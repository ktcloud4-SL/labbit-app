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

func TestArtifactRegionIsIndependentAndDefaultsToControlRegion(t *testing.T) {
	for _, tc := range []struct{ name, control, artifact, want string }{
		{"legacy Seoul", "ap-northeast-2", "", "ap-northeast-2"},
		{"legacy Osaka", "ap-northeast-3", "", "ap-northeast-3"},
		{"Seoul with Osaka archive", "ap-northeast-2", "ap-northeast-3", "ap-northeast-3"},
		{"Osaka with Seoul archive", "ap-northeast-3", "ap-northeast-2", "ap-northeast-2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			f.p.Region, f.p.Target.Region, f.p.ArtifactRegion = tc.control, tc.control, tc.artifact
			raw, ref := f.art.values["ob-version"], f.i.Evidence
			var calls []string
			var temp string
			a, err := NewAWSCLI(f.p, runnerFunc(func(_ context.Context, _ string, args []string) ([]byte, error) {
				calls = append(calls, args[0]+"/"+args[1])
				region := tc.want
				if args[0] == "sts" {
					region = tc.control
				} else if args[0] == "rds" {
					region = f.p.Database.Region
				}
				joined := strings.Join(args, " ")
				service := args[0]
				if service == "s3api" {
					service = "s3"
					if !strings.Contains(joined, "--version-id "+ref.VersionID) || !strings.Contains(joined, "--expected-bucket-owner "+f.p.AccountID) {
						t.Fatal("immutable reference or owner lost")
					}
				}
				if !strings.Contains(joined, "--region "+region+" ") || !strings.Contains(joined, "--endpoint-url https://"+service+"."+region+".amazonaws.com ") {
					t.Fatal("wrong service region or endpoint", calls)
				}
				switch args[0] + "/" + args[1] {
				case "sts/get-caller-identity":
					return jsonBytes(map[string]any{"Account": f.p.AccountID, "Arn": f.p.Actors[0].ARN}), nil
				case "rds/describe-db-instances":
					d := f.p.Database
					return jsonBytes(map[string]any{"DBInstances": []any{map[string]any{"DBInstanceArn": d.ARN, "DbiResourceId": d.ResourceID, "DBInstanceStatus": "available", "Engine": "postgres", "Endpoint": map[string]any{"Address": d.Host, "Port": d.Port}}}}), nil
				case "s3api/head-object":
					return jsonBytes(map[string]any{"VersionId": ref.VersionID, "ContentLength": len(raw), "ETag": "fixture"}), nil
				case "s3api/get-object":
					for j, s := range args {
						if s == "--range" {
							temp = args[j+2]
						}
					}
					if temp == "" || os.WriteFile(temp, raw, 0600) != nil {
						t.Fatal("download fixture failed")
					}
					return jsonBytes(map[string]any{"VersionId": ref.VersionID, "ContentLength": len(raw)}), nil
				default:
					t.Fatal("unexpected external operation")
					return nil, ErrUnavailable
				}
			}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.Identity(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err = a.VerifyDatabase(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, err := a.Read(context.Background(), ref)
			if err != nil || Hash(got) != ref.SHA256 || strings.Join(calls, ",") != "sts/get-caller-identity,rds/describe-db-instances,s3api/head-object,s3api/get-object" {
				t.Fatal("unexpected readback or calls", err, calls)
			}
			if _, err = os.Stat(temp); !os.IsNotExist(err) {
				t.Fatal("temporary artifact remains")
			}
		})
	}
}

func TestInvalidArtifactRegionRejectedBeforeExternalAccess(t *testing.T) {
	for _, region := range []string{"us-east-1", "https://s3.ap-northeast-3.amazonaws.com", "ap-northeast-3 ", "AP-NORTHEAST-3", "--endpoint-url"} {
		t.Run(region, func(t *testing.T) {
			f := setup(t)
			f.p.ArtifactRegion = region
			calls := 0
			a, err := NewAWSCLI(f.p, runnerFunc(func(context.Context, string, []string) ([]byte, error) { calls++; return nil, ErrUnavailable }))
			if f.p.Validate() == nil || err == nil || a != nil || calls != 0 {
				t.Fatal("unsupported artifact endpoint accepted")
			}
		})
	}
}

func TestCrossRegionArtifactRejectsCorruptionAndCancellation(t *testing.T) {
	for _, kind := range []string{"version", "hash", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			f.p.Region, f.p.Target.Region, f.p.ArtifactRegion = "ap-northeast-2", "ap-northeast-2", "ap-northeast-3"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var temp string
			calls := 0
			a, err := NewAWSCLI(f.p, runnerFunc(func(_ context.Context, _ string, args []string) ([]byte, error) {
				calls++
				ref := f.i.Evidence
				if args[1] == "head-object" {
					return jsonBytes(map[string]any{"VersionId": ref.VersionID, "ContentLength": ref.Size, "ETag": "fixture"}), nil
				}
				for j, s := range args {
					if s == "--range" {
						temp = args[j+2]
					}
				}
				raw := append([]byte(nil), f.art.values["ob-version"]...)
				if kind == "hash" {
					raw[0] ^= 1
				}
				if temp == "" || os.WriteFile(temp, raw, 0600) != nil {
					t.Fatal("fixture write failed")
				}
				version := ref.VersionID
				if kind == "version" {
					version = "other"
				}
				if kind == "cancel" {
					cancel()
				}
				return jsonBytes(map[string]any{"VersionId": version, "ContentLength": ref.Size}), nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.Read(ctx, f.i.Evidence); err == nil || calls != 2 {
				t.Fatal("corrupt or cancelled evidence accepted")
			}
			if _, err = os.Stat(temp); !os.IsNotExist(err) {
				t.Fatal("temporary artifact remains")
			}
		})
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
