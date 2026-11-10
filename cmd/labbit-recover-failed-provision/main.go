package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/operationrecovery"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Recovery refused; retain HOLD. Check target, lease and authenticated evidence.")
		os.Exit(1)
	}
}
func run() error {
	var cfg operationrecovery.SSHConfig
	item := flag.String("item", "", "Original HOLD operation item UUID")
	project := flag.String("project", "", "Operator-verified Provider project binding")
	operator := flag.String("operator", "", "Trusted infrastructure operator audit label (not a SaaS role)")
	reason := flag.String("reason", "", "Incident or change reference")
	apply := flag.String("apply-plan", "", "Apply only if freshly reconstructed plan equals this dry-run SHA256")
	tunnel := flag.String("database-tunnel", "", "Optional literal loopback host:port; preserves original database TLS hostname validation")
	flag.StringVar(&cfg.Address, "ssh-address", "", "Connector management host:port")
	flag.StringVar(&cfg.User, "ssh-user", "", "Operator SSH account")
	flag.StringVar(&cfg.HostAlias, "ssh-host-alias", "", "Already pinned Connector host alias")
	flag.StringVar(&cfg.KnownHosts, "ssh-known-hosts", "", "Protected existing known_hosts file")
	flag.StringVar(&cfg.PrivateKey, "ssh-private-key", "", "Operator SSH private key file")
	flag.StringVar(&cfg.Command, "ssh-command", "/usr/local/libexec/labbit-recovery-evidence", "Protected absolute executable wrapper, running as ledger owner")
	flag.Parse()
	if flag.NArg() != 0 {
		return operationrecovery.ErrUnsafe
	}
	id, err := uuid.Parse(*item)
	if err != nil {
		return operationrecovery.ErrUnsafe
	}
	environment := os.Getenv("LABBIT_ENVIRONMENT")
	if environment == "" {
		return operationrecovery.ErrUnsafe
	}
	dsn, err := postgres.LoadDSN(environment)
	if err != nil {
		return operationrecovery.ErrUnsafe
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	pool, err := recoveryPool(ctx, dsn, *tunnel)
	if err != nil {
		return operationrecovery.ErrUnsafe
	}
	defer pool.Close()
	store := postgres.NewStore(pool)
	target, err := store.FailedProvisionRecoveryTarget(ctx, id)
	if err != nil {
		return operationrecovery.ErrUnsafe
	}
	var nonce [32]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return operationrecovery.ErrUnsafe
	}
	request := operationrecovery.Request{OperationID: target.OperationID, InstanceID: target.InstanceID, ConnectionID: target.ConnectionID, Generation: target.Generation, VMKey: target.VMKey, ProjectID: *project, Nonce: hex.EncodeToString(nonce[:])}
	evidence, key, err := operationrecovery.Fetch(ctx, cfg, request)
	if err != nil {
		return operationrecovery.ErrUnsafe
	}
	plan, err := operationrecovery.Validate(target, request, evidence, key, *operator, *reason, time.Now().UTC())
	if err != nil {
		return operationrecovery.ErrUnsafe
	}
	if *apply != "" {
		if err = store.RecoverFailedProvision(ctx, plan, *apply); err != nil {
			return operationrecovery.ErrUnsafe
		}
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Plan       operationrecovery.Plan
		PlanSHA256 string
		Applied    bool
	}{plan, plan.Digest(), *apply != ""})
}
