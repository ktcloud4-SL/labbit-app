// Package drcontrol is an opt-in operator boundary, separate from public APIs.
// It authenticates before historical reads as well as mutations. It does not
// open maintenance, deploy Pods, prove fencing, or grant a global DR GO.
package drcontrol

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

var (
	ErrInput       = errors.New("drcontrol: invalid input")
	ErrDenied      = errors.New("drcontrol: identity or authorization not verified")
	ErrEvidence    = errors.New("drcontrol: evidence not verified")
	ErrUnavailable = errors.New("drcontrol: dependency not verified")
)

const Version = "labbit-dr-control/v1"
const MaxDocumentBytes = 256 << 10

var label = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var accountPattern = regexp.MustCompile(`^[0-9]{12}$`)

type Actor struct {
	ARN, Principal string
	Actions        []string
}
type Key struct {
	ID, Principal, PublicKey string
	Phases                   []string
}
type Database struct {
	Region, ARN, ResourceID, Host, Name, User string
	Port                                      uint16
}
type Policy struct {
	Version, AccountID, Region, AWSExecutable, AWSProfile string
	Target                                                wc.Scope
	Actors                                                []Actor
	ApprovalKeys, ObservationKeys                         []Key
	ArtifactBucket, ArtifactPrefix                        string
	ApprovalMaxSeconds, EvidenceMaxSeconds                int64
	Database                                              Database
	EnableExecution                                       bool
}
type Reference struct {
	Bucket, Key, VersionID, SHA256 string
	Size                           int64
}
type Invocation struct {
	Version, Action    string
	Request            wc.ResumeRequest
	Approval, Evidence Reference
}

// EvidenceBinding intentionally excludes PreconditionsSHA256. The immutable
// signed observation exists first; its raw hash is then included in Request.
// Approval binds the final Request SHA. This avoids a circular hash.
type EvidenceBinding struct {
	CommandID                                                 uuid.UUID
	Scope                                                     wc.Scope
	ExpectedRevision, ExpectedEpoch                           int64
	Desired                                                   wc.Mode
	TrialID, RunID, Phase, Principal, ApprovalRef, PlanSHA256 string
	RequestedAt, ValidUntil                                   time.Time
}

func Bind(r wc.ResumeRequest) EvidenceBinding {
	return EvidenceBinding{r.CommandID, r.Scope, r.ExpectedRevision, r.ExpectedEpoch, r.Desired, r.TrialID, r.RunID, r.Phase, r.Principal, r.ApprovalRef, r.PlanSHA256, r.RequestedAt, r.ValidUntil}
}

type Approval struct {
	Version, Action, RequestSHA256, Principal string
	ID                                        uuid.UUID
	NotBefore, ExpiresAt                      time.Time
}
type Observation struct {
	Version, Source                                                                     string
	Binding                                                                             EvidenceBinding
	ObservedAt, ValidUntil                                                              time.Time
	AdmissionBlocked, SingleWriter, Fencing, WorkInventory, PodCoverage, WorkReconciled string
}
type Signed struct {
	Version, KeyID string
	Payload        json.RawMessage
	Signature      string
}

func Hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func contains(values []string, wanted string) bool {
	for _, v := range values {
		if v == wanted {
			return true
		}
	}
	return false
}
func validTime(t time.Time) bool {
	return !t.IsZero() && t.Location() == time.UTC && t.Year() >= 1 && t.Year() <= 9999 && t.Equal(t.Truncate(time.Microsecond))
}

// Match encoding/json's Unicode simple-fold field equivalence, including the
// long-s and Kelvin-sign aliases that strings.ToLower alone does not normalize.
func foldedJSONKey(s string) string {
	var b strings.Builder
	for _, r := range s {
		minimum := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < minimum {
				minimum = next
			}
		}
		b.WriteRune(minimum)
	}
	return b.String()
}

// Strict rejects duplicate/unknown keys, trailing documents and invalid UTF-8.
// encoding/json alone accepts duplicate keys and replaces invalid UTF-8.
func Strict(b []byte, dst any) error {
	if len(b) == 0 || len(b) > MaxDocumentBytes || !utf8.Valid(b) {
		return ErrInput
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 32 {
			return ErrInput
		}
		token, err := d.Token()
		if err != nil {
			return ErrInput
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return ErrInput
				}
				s, ok := key.(string)
				// encoding/json resolves Go field names case-insensitively.
				// Treat aliases as duplicates before any struct decoding.
				folded := foldedJSONKey(s)
				if !ok || seen[folded] {
					return ErrInput
				}
				seen[folded] = true
				if walk(depth+1) != nil {
					return ErrInput
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim('}') {
				return ErrInput
			}
		case '[':
			for d.More() {
				if walk(depth+1) != nil {
					return ErrInput
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim(']') {
				return ErrInput
			}
		default:
			return ErrInput
		}
		return nil
	}
	if walk(0) != nil {
		return ErrInput
	}
	if _, e := d.Token(); e != io.EOF {
		return ErrInput
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil {
		return ErrInput
	}
	return nil
}

func (p Policy) Validate() error {
	if p.Version != Version || !accountPattern.MatchString(p.AccountID) || (p.Region != "ap-northeast-2" && p.Region != "ap-northeast-3") || p.Target.Validate() != nil || p.Target.Region != p.Region || p.AWSExecutable == "" || p.AWSProfile == "" || len(p.Actors) == 0 || len(p.ApprovalKeys) == 0 || len(p.ObservationKeys) == 0 || p.ApprovalMaxSeconds < 1 || p.ApprovalMaxSeconds > 86400 || p.EvidenceMaxSeconds < 1 || p.EvidenceMaxSeconds > 86400 || p.ArtifactBucket == "" || p.ArtifactPrefix == "" || strings.HasSuffix(p.ArtifactPrefix, "/") {
		return ErrInput
	}
	seen := map[string]bool{}
	principals := map[string]bool{}
	for _, a := range p.Actors {
		parts := strings.SplitN(a.ARN, ":", 6)
		if len(parts) != 6 || parts[0] != "arn" || parts[1] != "aws" || (parts[2] != "iam" && parts[2] != "sts") || parts[3] != "" || parts[4] != p.AccountID || !label.MatchString(a.Principal) || seen[a.ARN] || principals[a.Principal] || len(a.Actions) == 0 {
			return ErrInput
		}
		if !((parts[2] == "iam" && strings.HasPrefix(parts[5], "user/")) || (parts[2] == "sts" && strings.HasPrefix(parts[5], "assumed-role/"))) {
			return ErrInput
		}
		seen[a.ARN] = true
		principals[a.Principal] = true
		for _, action := range a.Actions {
			if action != "resume" && action != "inspect" {
				return ErrInput
			}
		}
	}
	allKeys := map[string]bool{}
	publicKeys := map[string]bool{}
	for _, keys := range [][]Key{p.ApprovalKeys, p.ObservationKeys} {
		for _, k := range keys {
			pub, err := base64.StdEncoding.DecodeString(k.PublicKey)
			if err != nil || len(pub) != ed25519.PublicKeySize || base64.StdEncoding.EncodeToString(pub) != k.PublicKey || !label.MatchString(k.ID) || !label.MatchString(k.Principal) || allKeys[k.ID] || publicKeys[string(pub)] || len(k.Phases) == 0 {
				return ErrInput
			}
			allKeys[k.ID] = true
			publicKeys[string(pub)] = true
			for _, phase := range k.Phases {
				if !label.MatchString(phase) {
					return ErrInput
				}
			}
		}
	}
	d := p.Database
	parts := strings.SplitN(d.ARN, ":", 6)
	if len(parts) != 6 || parts[0] != "arn" || parts[1] != "aws" || parts[2] != "rds" || parts[3] != d.Region || parts[4] != p.AccountID || !strings.HasPrefix(parts[5], "db:") || (d.Region != "ap-northeast-2" && d.Region != "ap-northeast-3") || d.ResourceID == "" || d.Host == "" || d.Port == 0 || d.Name == "" || d.User == "" {
		return ErrInput
	}
	return nil
}
func (p Policy) CheckReference(r Reference) error {
	if r.Bucket != p.ArtifactBucket || !strings.HasPrefix(r.Key, p.ArtifactPrefix+"/") || strings.ContainsAny(r.Key, "\r\n\x00") || r.VersionID == "" || r.VersionID == "null" || len(r.VersionID) > 1024 || !sha256Pattern.MatchString(r.SHA256) || r.Size < 1 || r.Size > MaxDocumentBytes {
		return ErrInput
	}
	return nil
}
func (p Policy) CheckInvocation(i Invocation) error {
	if p.Validate() != nil || i.Version != Version || (i.Action != "resume" && i.Action != "inspect") || i.Request.Validate() != nil || i.Request.Scope != p.Target {
		return ErrInput
	}
	if i.Action == "resume" && (p.CheckReference(i.Approval) != nil || p.CheckReference(i.Evidence) != nil || i.Request.PreconditionsSHA256 != i.Evidence.SHA256) {
		return ErrInput
	}
	return nil
}
