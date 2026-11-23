package drcontrol

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

type runnerFunc func(context.Context, string, []string) ([]byte, error)

func (f runnerFunc) Run(c context.Context, p string, a []string) ([]byte, error) { return f(c, p, a) }
func jsonBytes(v any) []byte                                                     { b, _ := json.Marshal(v); return b }
func TestFixedVersionReaderAndCleanup(t *testing.T) {
	f := setup(t)
	raw := f.art.values["ob-version"]
	ref := f.i.Evidence
	calls := 0
	temp := ""
	runner := runnerFunc(func(_ context.Context, p string, a []string) ([]byte, error) {
		calls++
		if p != f.p.AWSExecutable || !strings.Contains(strings.Join(a, " "), "--expected-bucket-owner 123456789012") || !strings.Contains(strings.Join(a, " "), "--version-id ob-version") || !strings.Contains(strings.Join(a, " "), "--endpoint-url https://s3.ap-northeast-3.amazonaws.com") {
			t.Fatal("unbounded AWS request")
		}
		if a[1] == "head-object" {
			return jsonBytes(map[string]any{"VersionId": ref.VersionID, "ContentLength": len(raw), "ETag": "\"fixture\""}), nil
		}
		if a[1] != "get-object" {
			t.Fatal("unexpected mutation")
		}
		for j, s := range a {
			if s == "--range" {
				temp = a[j+2]
			}
		}
		if temp == "" {
			t.Fatal("range missing")
		}
		if e := os.WriteFile(temp, raw, 0600); e != nil {
			t.Fatal(e)
		}
		return jsonBytes(map[string]any{"VersionId": ref.VersionID, "ContentLength": len(raw)}), nil
	})
	a, e := NewAWSCLI(f.p, runner)
	if e != nil {
		t.Fatal(e)
	}
	got, e := a.Read(context.Background(), ref)
	if e != nil || Hash(got) != ref.SHA256 || calls != 2 {
		t.Fatal("version readback failed", e)
	}
	if _, e = os.Stat(temp); !os.IsNotExist(e) {
		t.Fatal("temporary artifact remains")
	}
}
func TestHeadMismatchStopsBeforeDownload(t *testing.T) {
	for _, kind := range []string{"version", "size", "delete"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			calls := 0
			a, _ := NewAWSCLI(f.p, runnerFunc(func(context.Context, string, []string) ([]byte, error) {
				calls++
				r := map[string]any{"VersionId": f.i.Evidence.VersionID, "ContentLength": f.i.Evidence.Size, "ETag": "etag"}
				switch kind {
				case "version":
					r["VersionId"] = "other"
				case "size":
					r["ContentLength"] = MaxDocumentBytes + 1
				case "delete":
					r["DeleteMarker"] = true
				}
				return jsonBytes(r), nil
			}))
			if _, e := a.Read(context.Background(), f.i.Evidence); e == nil || calls != 1 {
				t.Fatal("invalid HEAD followed by download")
			}
		})
	}
}
func TestReaderHonorsCancellationEvenIfRunnerReturnsSuccess(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	a, _ := NewAWSCLI(f.p, runnerFunc(func(context.Context, string, []string) ([]byte, error) {
		cancel()
		return []byte(`{"Account":"123456789012","Arn":"fixture"}`), nil
	}))
	if _, e := a.Identity(ctx); e == nil {
		t.Fatal("cancelled response accepted")
	}
}
func TestReaderBoundedContext(t *testing.T) {
	f := setup(t)
	a, _ := NewAWSCLI(f.p, runnerFunc(func(c context.Context, _ string, _ []string) ([]byte, error) { <-c.Done(); return nil, c.Err() }))
	c, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, e := a.Identity(c); e == nil {
		t.Fatal("timeout accepted")
	}
}
func TestRDSIdentityEndpointAndEngine(t *testing.T) {
	for _, kind := range []string{"valid", "resource", "host", "replica engine", "status"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			d := f.p.Database
			instance := map[string]any{"DBInstanceArn": d.ARN, "DbiResourceId": d.ResourceID, "DBInstanceStatus": "available", "Engine": "postgres", "Endpoint": map[string]any{"Address": d.Host, "Port": d.Port}}
			switch kind {
			case "resource":
				instance["DbiResourceId"] = "replaced"
			case "host":
				instance["Endpoint"] = map[string]any{"Address": "other", "Port": d.Port}
			case "replica engine":
				instance["Engine"] = "aurora-postgresql"
			case "status":
				instance["DBInstanceStatus"] = "modifying"
			}
			a, _ := NewAWSCLI(f.p, runnerFunc(func(_ context.Context, _ string, args []string) ([]byte, error) {
				if args[0] != "rds" || args[1] != "describe-db-instances" || args[3] != d.ARN {
					t.Fatal("unexpected DB action")
				}
				return jsonBytes(map[string]any{"DBInstances": []any{instance}}), nil
			}))
			e := a.VerifyDatabase(context.Background())
			if (e == nil) != (kind == "valid") {
				t.Fatal("RDS target verification", e)
			}
		})
	}
}
