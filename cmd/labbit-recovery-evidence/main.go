// labbit-recovery-evidence runs as the Connector ledger owner with the same
// protected Provider environment. Invoke only through the pinned operator SSH
// channel. It has no listener, SaaS credential, mutation or DB connection.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	openstackprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider/openstack"
	"github.com/ktcloud4-SL/labbit-app/internal/operationrecovery"
)

func main() {
	if run() != nil {
		fmt.Fprintln(os.Stderr, "Recovery evidence unavailable; retain HOLD")
		os.Exit(1)
	}
}
func run() error {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, 4097))
	if err != nil || len(data) > 4096 {
		return operationrecovery.ErrUnsafe
	}
	var req operationrecovery.Request
	if json.Unmarshal(data, &req) != nil {
		return operationrecovery.ErrUnsafe
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	a, err := openstackprovider.New(ctx, openstackprovider.ConfigFromEnvironment())
	if err != nil {
		return operationrecovery.ErrUnsafe
	}
	proof, err := a.FailedProvisionEvidence(ctx, req)
	if err != nil {
		return operationrecovery.ErrUnsafe
	}
	return json.NewEncoder(os.Stdout).Encode(proof)
}
