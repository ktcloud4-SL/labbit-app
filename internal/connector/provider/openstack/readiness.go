package openstackprovider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"

	"os"
	"strings"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
	"golang.org/x/crypto/ssh"
)

// probeWorkspacePTY uses the same authenticated, pinned PTY/shell implementation
// as Workspace Terminal. Only a bounded ephemeral marker is inspected; no shell
// content, credentials or transcript is persisted or logged.
func (a *Adapter) probeWorkspacePTY(ctx context.Context, address, serverID string) error {
	key, err := os.ReadFile(a.provision.SSHPrivateKeyFile)
	if err != nil {
		return ErrSSHNotReady
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return ErrSSHNotReady
	}
	callback, err := a.SSHHostKeyCallback(ctx, serverID)
	if err != nil {
		return ErrSSHNotReady
	}
	pty, err := terminal.DialSSHPTY(ctx, address, &ssh.ClientConfig{User: a.provision.SSHUsername, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: callback}, 80, 24)
	if err != nil {
		return ErrSSHNotReady
	}
	defer pty.Close()
	stop := context.AfterFunc(ctx, func() { _ = pty.Close() })
	defer stop()
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return ErrSSHNotReady
	}
	marker := hex.EncodeToString(nonce)
	// The complete expected marker never appears in the input, so terminal echo
	// cannot masquerade as shell execution.
	command := "printf '%s%s\\n' '" + marker[:16] + "' '" + marker[16:] + "'\n"
	done := make(chan error, 1)
	go func() { _, e := pty.Write([]byte(command)); done <- e }()
	select {
	case err = <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err != nil {
		return ErrSSHNotReady
	}
	data := make([]byte, 0, 16*1024)
	buf := make([]byte, 1024)
	for len(data) < 16*1024 {
		n, e := pty.Read(buf)
		data = append(data, buf[:n]...)
		if strings.Contains(string(data), marker) && ctx.Err() == nil {
			return nil
		}
		if e != nil {
			return errors.Join(ErrSSHNotReady, e, ctx.Err())
		}
	}
	return ErrSSHNotReady
}
