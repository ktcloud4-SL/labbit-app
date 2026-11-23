// This opt-in operator binary is never called by labbit-server startup.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ktcloud4-SL/labbit-app/internal/drcontrol"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
)

type result struct {
	Status, Reason, ExecutionGate     string
	RecoveryAuthorized, CloudVerified bool
	CommandID, RequestSHA256          string
}

func read(path string) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, drcontrol.ErrInput
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, drcontrol.MaxDocumentBytes+1))
	if e != nil || len(b) > drcontrol.MaxDocumentBytes {
		return nil, drcontrol.ErrInput
	}
	return b, nil
}
func run(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("labbit-dr-control", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	policyFile := fs.String("policy", "", "protected policy JSON")
	inputFile := fs.String("input", "", "immutable invocation JSON")
	dsnFile := fs.String("dsn-file", "", "protected DSN file; execute only")
	execute := fs.Bool("execute", false, "explicit opt-in; preview is the default")
	response := result{Status: "NOT_VERIFIED", Reason: "INPUT_NOT_VERIFIED", ExecutionGate: "HOLD"}
	finish := func(code int) int { _ = json.NewEncoder(out).Encode(response); return code }
	if fs.Parse(args) != nil || fs.NArg() != 0 {
		return finish(2)
	}
	pb, e := read(*policyFile)
	if e != nil {
		return finish(2)
	}
	ib, e := read(*inputFile)
	if e != nil {
		return finish(2)
	}
	var p drcontrol.Policy
	var i drcontrol.Invocation
	var restricted drcontrol.RestrictInvocation
	var header struct{ Action string }
	if json.Unmarshal(ib, &header) != nil {
		return finish(2)
	}
	isRestriction := header.Action == "init" || header.Action == "pause" || header.Action == "inspect-restriction"
	if drcontrol.Strict(pb, &p) != nil {
		return finish(2)
	}
	if isRestriction {
		if drcontrol.Strict(ib, &restricted) != nil || p.CheckRestriction(restricted) != nil {
			return finish(2)
		}
		response.CommandID = restricted.Request.CommandID.String()
		response.RequestSHA256 = restricted.Request.SHA256()
	} else if drcontrol.Strict(ib, &i) != nil || p.CheckInvocation(i) != nil {
		return finish(2)
	}
	if !isRestriction {
		response.CommandID = i.Request.CommandID.String()
		response.RequestSHA256 = i.Request.SHA256()
	}
	if !*execute {
		response.Status = "PREVIEW_ONLY"
		response.Reason = "NO_EXTERNAL_IO"
		return finish(0)
	}
	if !p.EnableExecution {
		response.Reason = "EXECUTION_DISABLED"
		return finish(2)
	}
	protected, e := drcontrol.ReadProtected(*policyFile)
	if e != nil || string(protected) != string(pb) {
		response.Reason = "POLICY_NOT_VERIFIED"
		return finish(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cloud, e := drcontrol.NewAWSCLI(p, drcontrol.CommandRunner{})
	if e != nil {
		response.Reason = "POLICY_NOT_VERIFIED"
		return finish(2)
	}
	authErr := error(nil)
	if isRestriction {
		authErr = drcontrol.AuthenticateRestriction(ctx, p, restricted, cloud)
	} else {
		authErr = drcontrol.Authenticate(ctx, p, i, cloud)
	}
	if authErr != nil {
		response.Reason = "IDENTITY_NOT_VERIFIED"
		return finish(2)
	}
	if cloud.VerifyDatabase(ctx) != nil {
		response.Reason = "DATABASE_TARGET_NOT_VERIFIED"
		return finish(2)
	}
	raw, e := drcontrol.ReadProtected(*dsnFile)
	if e != nil {
		response.Reason = "DATABASE_CONFIG_NOT_VERIFIED"
		return finish(2)
	}
	config, e := drcontrol.DatabaseConfig(p, strings.TrimSpace(string(raw)))
	if e != nil {
		response.Reason = "DATABASE_CONFIG_NOT_VERIFIED"
		return finish(2)
	}
	pool, e := pgxpool.NewWithConfig(ctx, config)
	if e != nil {
		response.Reason = "DATABASE_NOT_VERIFIED"
		return finish(2)
	}
	defer pool.Close()
	if drcontrol.VerifySQL(ctx, pool, p.Database) != nil {
		response.Reason = "DATABASE_WRITER_NOT_VERIFIED"
		return finish(2)
	}
	if isRestriction {
		record, e := drcontrol.ExecuteRestriction(ctx, p, restricted, cloud, cloud, postgres.NewWorkerControlStore(pool), func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) })
		if e != nil {
			response.Reason = "RESTRICTION_NOT_VERIFIED_NO_AUTO_RETRY"
			if record.Status != "" {
				response.Status = string(record.Status)
			}
			return finish(2)
		}
		response.Status = string(record.Status)
		response.Reason = "PAUSE_RECEIPT_ONLY_NOT_DRAIN_OR_FENCING"
		return finish(0)
	}
	record, e := drcontrol.Execute(ctx, p, i, cloud, cloud, postgres.NewWorkerControlStore(pool), func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) })
	if e != nil {
		response.Reason = "CONTROL_NOT_VERIFIED_NO_AUTO_RETRY"
		if record.Status != "" {
			response.Status = string(record.Status)
		}
		return finish(2)
	}
	response.Status = string(record.Status)
	response.Reason = "HISTORICAL_RECEIPT_ONLY_NO_DEPLOYMENT"
	return finish(0)
}
func main() {
	if code := run(os.Args[1:], os.Stdout); code != 0 {
		fmt.Fprintln(os.Stderr, "DR control remains HOLD; no automatic retry")
		os.Exit(code)
	}
}
