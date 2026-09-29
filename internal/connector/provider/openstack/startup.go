package openstackprovider

import (
	"context"
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

func (a *Adapter) waitStartupReady(ctx context.Context, port ports.Port) error {
	address, err := managementAddress(port)
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
		if err := probe(ctx, net.JoinHostPort(address, "22")); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(ErrStartupNotReady, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (a *Adapter) probeCloudInitComplete(ctx context.Context, address string) error {
	config := normalizedProvisionConfig(a.provision)
	privateKey, err := os.ReadFile(config.SSHPrivateKeyFile)
	if err != nil {
		return ErrStartupNotReady
	}
	signer, err := ssh.ParsePrivateKey(privateKey)
	if err != nil {
		return ErrStartupNotReady
	}
	hostKeyCallback, err := trustOnFirstUseCallback(config.SSHKnownHostsFile)
	if err != nil {
		return ErrStartupNotReady
	}

	dialer := net.Dialer{Timeout: 5 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return ErrStartupNotReady
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}

	clientConnection, channels, requests, err := ssh.NewClientConn(connection, address, &ssh.ClientConfig{
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
	if err := session.Run("cloud-init status --wait"); err != nil {
		return ErrStartupNotReady
	}
	return nil
}

func trustOnFirstUseCallback(path string) (ssh.HostKeyCallback, error) {
	path = strings.TrimSpace(path)
	if path == "" {
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
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := verify(hostname, remote, key)
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
		freshErr = freshVerify(hostname, remote, key)
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
		_, writeErr := file.WriteString(knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key) + "\n")
		if writeErr != nil {
			return ErrStartupNotReady
		}
		return nil
	}, nil
}
