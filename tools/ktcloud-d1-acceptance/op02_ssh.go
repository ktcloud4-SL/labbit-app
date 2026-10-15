package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	openstackprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider/openstack"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"golang.org/x/crypto/ssh"
)

// Fixture-only privileged installation, after the production Provider enrolled
// the exact Server pin. Runtime File operations still use the rooted helper.
func workspaceExec(ctx context.Context, server string, input []byte, command string) ([]byte, error) {
	wait, cancel := bounded(ctx, 40)
	defer cancel()
	adapter, err := openstackprovider.New(wait, openstackprovider.ConfigFromEnvironment())
	if err != nil {
		return nil, errors.New("Workspace fixture Provider unavailable")
	}
	m := vmaccess.NewManager(adapter, vmaccess.Config{SetupTimeout: 40 * time.Second})
	defer m.Close()
	s, err := m.OpenSession(wait, vmaccess.Target{LabInstanceID: journal.LabID, Generation: serverGeneration(server), VMKey: "workspace", ServerID: server})
	if err != nil {
		if wait.Err() != nil {
			return nil, errors.New("Workspace fixture pinned SSH setup deadline exceeded")
		}
		if errors.Is(err, vmaccess.ErrInvalidTarget) {
			return nil, errors.New("Workspace fixture target invalid or stale")
		}
		return nil, errors.New("Workspace fixture pinned SSH unavailable")
	}
	defer s.Close()
	var output bytes.Buffer
	err = s.Setup(wait, func(raw *ssh.Session) error {
		raw.Stdin = bytes.NewReader(input)
		raw.Stdout = &output
		raw.Stderr = io.Discard
		return raw.Run(command)
	})
	if err != nil {
		return nil, errors.New("Workspace fixture command failed; details withheld")
	}
	return output.Bytes(), nil
}
func serverGeneration(server string) int64 {
	for _, r := range journal.Resources {
		if r.ResourceType == "SERVER" && r.ProviderID == server {
			return r.Generation
		}
	}
	return 0
}
func installWorkspace(ctx context.Context, server string) error {
	b, err := os.ReadFile(filepath.Join(os.Getenv("LBT145_CONTROL_DIRECTORY"), "labbit-workspace-sftp"))
	if err != nil || len(b) < 4 || string(b[:4]) != "\x7fELF" {
		return errors.New("Latest Linux Workspace helper missing")
	}
	cmd := "umask 077; cat > /home/ubuntu/op02-helper-upload && sudo -n install -d -o root -g root -m 0755 /usr/local/libexec /var/lib/labbit-workspaces && sudo -n install -o root -g root -m 0755 /home/ubuntu/op02-helper-upload /usr/local/libexec/labbit-workspace-sftp-real && sudo -n install -o root -g root -m 0755 /bin/sleep /usr/local/libexec/labbit-op02-delay && sudo -n install -d -o ubuntu -g ubuntu -m 0750 /var/lib/labbit-workspaces/workspace && rm /home/ubuntu/op02-helper-upload"
	if _, err = workspaceExec(ctx, server, b, cmd); err != nil {
		return err
	}
	// Preserve the real helper unchanged behind a root-owned fault-injection
	// wrapper. A delay process is exec'd (no sleep child orphan on SIGKILL).
	wrapper := []byte("#!/bin/sh\nset -eu\nprintf '%s' \"$SSH_CONNECTION\" > /var/lib/labbit-workspaces/workspace/.helper-connection\nif test -f /var/lib/labbit-workspaces/workspace/.delay; then exec /usr/local/libexec/labbit-op02-delay 120; fi\nexec /usr/local/libexec/labbit-workspace-sftp-real \"$@\"\n")
	if _, err = workspaceExec(ctx, server, wrapper, "umask 077; cat > /home/ubuntu/op02-wrapper-upload && sudo -n install -o root -g root -m 0755 /home/ubuntu/op02-wrapper-upload /usr/local/libexec/labbit-workspace-sftp && rm /home/ubuntu/op02-wrapper-upload"); err != nil {
		return err
	}
	cmd = "set -eu; test \"$(stat -c '%u:%a' /usr/local/libexec/labbit-workspace-sftp)\" = '0:755'; test \"$(stat -c '%u:%a' /usr/local/libexec/labbit-workspace-sftp-real)\" = '0:755'; cd /var/lib/labbit-workspaces/workspace; mkdir dir; printf initial > file; : > empty; truncate -s 1048577 large; printf outside > /home/ubuntu/op02-outside; ln -s /home/ubuntu/op02-outside link; sudo -n sh -c 'printf denied > /var/lib/labbit-workspaces/workspace/denied; chmod 000 /var/lib/labbit-workspaces/workspace/denied'"
	if _, err = workspaceExec(ctx, server, nil, cmd); err != nil {
		return err
	}
	output, err := workspaceExec(ctx, server, nil, "sha256sum /usr/local/libexec/labbit-workspace-sftp-real")
	digest := sha256.Sum256(b)
	fields := strings.Fields(string(output))
	if err != nil || len(fields) != 2 || fields[0] != hex.EncodeToString(digest[:]) {
		return errors.New("Installed Workspace helper digest differs")
	}
	fmt.Printf("Installed Workspace helper g%d: latest binary SHA256=%s; helper/root owner and mode checked; pinned SSH used\n", serverGeneration(server), fields[0])
	return nil
}
func delayedHelper(ctx context.Context, server string, enabled bool) error {
	cmd := "rm -f " + workspaceRoot + "/.delay"
	if enabled {
		cmd = "touch " + workspaceRoot + "/.delay"
	}
	_, err := workspaceExec(ctx, server, nil, cmd)
	return err
}
func helperCount(ctx context.Context, server string) (int, error) {
	b, err := workspaceExec(ctx, server, nil, "pgrep -f '^/usr/local/libexec/labbit-op02-delay 120$' | wc -l")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n < 0 {
		return 0, errors.New("Owned helper process count invalid")
	}
	return n, nil
}

func waitHelperCount(ctx context.Context, server string, expected int) error {
	wait, cancel := bounded(ctx, 15)
	defer cancel()
	for {
		n, err := helperCount(wait, server)
		if err != nil {
			return err
		}
		if n == expected {
			return nil
		}
		select {
		case <-wait.Done():
			return errors.New("Concurrent owned helper count not observed")
		case <-time.After(100 * time.Millisecond):
		}
	}
}
func waitHelper(ctx context.Context, server string, present bool) error {
	wait, cancel := bounded(ctx, 15)
	defer cancel()
	for {
		n, err := helperCount(wait, server)
		if err != nil {
			return err
		}
		if (n > 0) == present {
			return nil
		}
		select {
		case <-wait.Done():
			return errors.New("Owned delay helper drain/start not observed")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (r *op02Run) idle(ctx context.Context, server string, generation int64) error {
	identity := strings.Fields(r.sshIdentities[generation])
	if len(identity) != 4 {
		return errors.New("SSH transport identity absent")
	}
	peer := net.JoinHostPort(identity[0], identity[1])
	present := func() (bool, error) {
		output, err := workspaceExec(ctx, server, nil, "ss -tnH state established '( sport = :22 )'")
		if err != nil {
			return false, err
		}
		for _, line := range strings.Split(string(output), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 4 && fields[len(fields)-1] == peer {
				return true, nil
			}
		}
		return false, nil
	}
	active, err := present()
	if err != nil || !active {
		return errors.New("Idle transport initial cached connection absent")
	}
	// Default production idle timeout is one minute; no shortened runtime knobs.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(65 * time.Second):
	}
	active, err = present()
	if err != nil || active {
		return errors.New("Production idle SSH transport was not retired")
	}
	return save("OP02_PRODUCTION_60_SECOND_IDLE_SHARED_SSH_SOCKET_RETIRED_PASS")
}
