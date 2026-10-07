package openstackprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

var (
	ErrStartupNotReady = errors.New("VM initialization did not complete")
	knownHostsMu       sync.Mutex
)

func (a *Adapter) waitStartupReady(ctx context.Context, port ports.Port, serverID string) error {
	address, err := managementAddress(port)
	if err != nil {
		return err
	}
	hostIdentity, err := sshHostKeyIdentity(a.provision.ProviderConnectionID, serverID)
	if err != nil {
		return err
	}
	probe := a.startupProbe
	if probe == nil {
		probe = a.probeCloudInitComplete
	}
	ticker := time.NewTicker(a.provision.PollInterval)
	defer ticker.Stop()
	for {
		if err := probe(ctx, net.JoinHostPort(address, "22"), hostIdentity); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(ErrStartupNotReady, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (a *Adapter) probeCloudInitComplete(ctx context.Context, address, hostIdentity string) error {
	return a.runSSHCommand(ctx, address, hostIdentity, "cloud-init status --wait")
}

func (a *Adapter) runSSHCommand(ctx context.Context, address, hostIdentity, command string) error {
	config := normalizedProvisionConfig(a.provision)
	privateKey, err := os.ReadFile(config.SSHPrivateKeyFile)
	if err != nil {
		return ErrStartupNotReady
	}
	signer, err := ssh.ParsePrivateKey(privateKey)
	if err != nil {
		return ErrStartupNotReady
	}
	hostKeyCallback, err := trustOnFirstUseCallback(config.SSHKnownHostsFile, hostIdentity)
	if err != nil {
		return ErrStartupNotReady
	}

	dialer := net.Dialer{Timeout: 5 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return ErrStartupNotReady
	}
	defer connection.Close()
	// DialContext only cancels connection establishment. Once connected, also
	// close the socket on cancellation to interrupt SSH handshake/exec reads.
	stopCancelClose := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopCancelClose()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}

	clientConnection, channels, requests, err := ssh.NewClientConn(connection, hostIdentity, &ssh.ClientConfig{
		User:            config.SSHUsername,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeyCallback,
	})
	if err != nil {
		return ErrStartupNotReady
	}
	client := ssh.NewClient(clientConnection, channels, requests)
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return ErrStartupNotReady
	}
	defer session.Close()
	if err := session.Run(command); err != nil {
		return ErrStartupNotReady
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrStartupNotReady, err)
	}
	return nil
}

func sshHostKeyIdentity(providerConnectionID, serverID string) (string, error) {
	providerConnectionID = strings.TrimSpace(providerConnectionID)
	serverID = strings.TrimSpace(serverID)
	if providerConnectionID == "" || serverID == "" {
		return "", ErrStartupNotReady
	}
	digest := sha256.Sum256([]byte(providerConnectionID + "\x00" + serverID))
	return "labbit-server-" + hex.EncodeToString(digest[:]) + ":22", nil
}

func trustOnFirstUseCallback(path, hostIdentity string) (ssh.HostKeyCallback, error) {
	path = strings.TrimSpace(path)
	hostIdentity = strings.TrimSpace(hostIdentity)
	if path == "" || hostIdentity == "" {
		return nil, ErrStartupNotReady
	}
	knownHostsMu.Lock()
	defer knownHostsMu.Unlock()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, ErrStartupNotReady
	}
	if err := file.Close(); err != nil {
		return nil, ErrStartupNotReady
	}
	verify, err := knownhosts.New(path)
	if err != nil {
		return nil, ErrStartupNotReady
	}
	return func(_ string, remote net.Addr, key ssh.PublicKey) error {
		err := verify(hostIdentity, remote, key)
		if err == nil {
			return nil
		}
		var keyError *knownhosts.KeyError
		if !errors.As(err, &keyError) || len(keyError.Want) != 0 {
			return ErrStartupNotReady
		}
		knownHostsMu.Lock()
		defer knownHostsMu.Unlock()
		// Another operation may have pinned this host after this callback was
		// created. Re-read the file under the lock so only one first key wins.
		freshVerify, freshErr := knownhosts.New(path)
		if freshErr != nil {
			return ErrStartupNotReady
		}
		freshErr = freshVerify(hostIdentity, remote, key)
		if freshErr == nil {
			return nil
		}
		var freshKeyError *knownhosts.KeyError
		if !errors.As(freshErr, &freshKeyError) || len(freshKeyError.Want) != 0 {
			return ErrStartupNotReady
		}
		file, openErr := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		if openErr != nil {
			return ErrStartupNotReady
		}
		defer file.Close()
		_, writeErr := file.WriteString(knownhosts.Line([]string{knownhosts.Normalize(hostIdentity)}, key) + "\n")
		if writeErr != nil {
			return ErrStartupNotReady
		}
		return nil
	}, nil
}
