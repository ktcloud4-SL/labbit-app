package drcontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Runner is a bounded command boundary, not a shell. AWS credentials and raw
// stderr are never returned. Only this package constructs service arguments.
type Runner interface {
	Run(context.Context, string, []string) ([]byte, error)
}
type CommandRunner struct{}
type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, ErrUnavailable
	}
	return b.Buffer.Write(p)
}
func (CommandRunner) Run(ctx context.Context, executable string, args []string) ([]byte, error) {
	if !filepath.IsAbs(executable) || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	for _, e := range os.Environ() {
		name, _, _ := strings.Cut(e, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "AWS_ENDPOINT_URL") && name != "AWS_PAGER" && name != "AWS_CLI_AUTO_PROMPT" && name != "AWS_MAX_ATTEMPTS" && name != "AWS_IGNORE_CONFIGURED_ENDPOINT_URLS" {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "AWS_PAGER=", "AWS_CLI_AUTO_PROMPT=off", "AWS_MAX_ATTEMPTS=1", "AWS_IGNORE_CONFIGURED_ENDPOINT_URLS=true")
	out := &limitedBuffer{limit: MaxDocumentBytes}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if cmd.Run() != nil || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	return out.Bytes(), nil
}

type AWSCLI struct {
	policy Policy
	runner Runner
}

func NewAWSCLI(p Policy, r Runner) (*AWSCLI, error) {
	if p.Validate() != nil || r == nil || !filepath.IsAbs(p.AWSExecutable) {
		return nil, ErrInput
	}
	return &AWSCLI{p, r}, nil
}
func (a *AWSCLI) call(ctx context.Context, service, region string, args ...string) ([]byte, error) {
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	endpoint := "https://" + service + "." + region + ".amazonaws.com"
	if service == "s3api" {
		endpoint = "https://s3." + region + ".amazonaws.com"
	}
	argv := []string{service}
	argv = append(argv, args...)
	argv = append(argv, "--profile", a.policy.AWSProfile, "--region", region, "--endpoint-url", endpoint, "--output", "json", "--no-cli-pager", "--no-cli-auto-prompt", "--cli-connect-timeout", "1", "--cli-read-timeout", "1")
	b, e := a.runner.Run(c, a.policy.AWSExecutable, argv)
	if e != nil || c.Err() != nil || len(b) > MaxDocumentBytes {
		return nil, ErrUnavailable
	}
	return b, nil
}
func (a *AWSCLI) Identity(ctx context.Context) (Identity, error) {
	b, e := a.call(ctx, "sts", a.policy.Region, "get-caller-identity")
	if e != nil {
		return Identity{}, e
	}
	var v struct{ Account, Arn string }
	if json.Unmarshal(b, &v) != nil || v.Account == "" || v.Arn == "" {
		return Identity{}, ErrUnavailable
	}
	return Identity{v.Account, v.Arn}, nil
}

func (a *AWSCLI) Read(ctx context.Context, r Reference) ([]byte, error) {
	if a.policy.CheckReference(r) != nil {
		return nil, ErrEvidence
	}
	region := a.policy.ArtifactRegion
	if region == "" {
		region = a.policy.Region
	}
	args := []string{"--bucket", r.Bucket, "--key", r.Key, "--version-id", r.VersionID, "--expected-bucket-owner", a.policy.AccountID}
	b, e := a.call(ctx, "s3api", region, append([]string{"head-object"}, args...)...)
	if e != nil {
		return nil, ErrEvidence
	}
	var meta struct {
		VersionId, ETag string
		ContentLength   int64
		DeleteMarker    bool
	}
	if json.Unmarshal(b, &meta) != nil || meta.VersionId != r.VersionID || meta.ContentLength != r.Size || meta.DeleteMarker || meta.ETag == "" {
		return nil, ErrEvidence
	}
	f, e := os.CreateTemp("", "labbit-dr-verified-*.json")
	if e != nil {
		return nil, ErrUnavailable
	}
	name := f.Name()
	defer os.Remove(name)
	if f.Close() != nil {
		return nil, ErrUnavailable
	}
	get := append([]string{"get-object"}, args...)
	get = append(get, "--if-match", meta.ETag, "--range", "bytes=0-"+strconv.FormatInt(r.Size-1, 10), name)
	b, e = a.call(ctx, "s3api", region, get...)
	if e != nil {
		return nil, ErrEvidence
	}
	var got struct {
		VersionId     string
		ContentLength int64
		DeleteMarker  bool
	}
	if json.Unmarshal(b, &got) != nil || got.VersionId != r.VersionID || got.ContentLength != r.Size || got.DeleteMarker {
		return nil, ErrEvidence
	}
	file, e := os.Open(name)
	if e != nil {
		return nil, ErrEvidence
	}
	defer file.Close()
	raw, e := io.ReadAll(io.LimitReader(file, r.Size+1))
	if e != nil || int64(len(raw)) != r.Size || Hash(raw) != r.SHA256 || ctx.Err() != nil {
		return nil, ErrEvidence
	}
	return raw, nil
}

// VerifyDatabase identifies the configured instance; it is not Single Writer
// proof. SQL recovery status is checked separately, and fencing is still required.
func (a *AWSCLI) VerifyDatabase(ctx context.Context) error {
	d := a.policy.Database
	b, e := a.call(ctx, "rds", d.Region, "describe-db-instances", "--db-instance-identifier", d.ARN)
	if e != nil {
		return ErrUnavailable
	}
	var v struct {
		DBInstances []struct {
			DBInstanceArn, DbiResourceId, DBInstanceStatus, Engine string
			Endpoint                                               struct {
				Address string
				Port    uint16
			}
		}
	}
	if json.Unmarshal(b, &v) != nil || len(v.DBInstances) != 1 {
		return ErrUnavailable
	}
	r := v.DBInstances[0]
	if r.DBInstanceArn != d.ARN || r.DbiResourceId != d.ResourceID || r.DBInstanceStatus != "available" || r.Engine != "postgres" || r.Endpoint.Address != d.Host || r.Endpoint.Port != d.Port {
		return ErrDenied
	}
	return nil
}
