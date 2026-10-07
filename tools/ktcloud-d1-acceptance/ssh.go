package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	openstackprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider/openstack"
	"golang.org/x/crypto/ssh"
)

// Diagnostic after a real WSS operation succeeded; never replaces its result.
func proveManagementSSH(ctx context.Context, items []protocol.ProviderResourceRef, generation int64) error {
	serverID := ""
	for _, item := range items {
		if item.ResourceType == "SERVER" {
			serverID = item.ProviderID
		}
	}
	adapter, err := openstackprovider.New(ctx, openstackprovider.ConfigFromEnvironment())
	if err != nil {
		return errors.New("management diagnostic Provider unavailable")
	}
	address, err := adapter.ResolveServerAddress(ctx, "workspace", serverID)
	if err != nil {
		return errors.New("Provider-owned Management target resolution failed")
	}
	callback, err := adapter.SSHHostKeyCallback(ctx, serverID)
	if err != nil {
		return errors.New("already enrolled SSH pin unavailable")
	}
	key, err := os.ReadFile(os.Getenv("LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE"))
	if err != nil {
		return errors.New("local private key unavailable")
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return errors.New("local private key invalid")
	}
	connection, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(address, "22"))
	if err != nil {
		return errors.New("Management TCP22 unavailable")
	}
	defer connection.Close()
	connection.SetDeadline(time.Now().Add(30 * time.Second))
	cc, channels, requests, err := ssh.NewClientConn(connection, address, &ssh.ClientConfig{User: "ubuntu", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: callback})
	if err != nil {
		return errors.New("pinned management SSH authentication failed")
	}
	client := ssh.NewClient(cc, channels, requests)
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return errors.New("authenticated SSH exec unavailable")
	}
	defer session.Close()
	command := "python3 - <<'PY'\nimport socket,sys\nblocked=[]\nfor port in (22,443):\n try:\n  connection=socket.create_connection(('1.1.1.1',port),timeout=4)\n  connection.close(); blocked.append(False)\n except (TimeoutError,OSError):\n  blocked.append(True)\nsys.exit(0 if all(blocked) else 1)\nPY"
	if session.Run(command) != nil {
		return errors.New("InternetOutbound=false external TCP denial was not verified")
	}
	fmt.Printf("Management g%d: Provider-owned IP resolved; pinned key-authenticated SSH exec PASS; external TCP22/443 denied PASS\n", generation)
	return save(fmt.Sprintf("GENERATION%d_MANAGEMENT_IP_PINNED_SSH_EGRESS_DENY_PASS", generation))
}
