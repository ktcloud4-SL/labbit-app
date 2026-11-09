package operationrecovery

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"regexp"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type SSHConfig struct{ Address, User, HostAlias, KnownHosts, PrivateKey, Command string }

// Fetch has no shell interpolation of request fields, agent fallback, TOFU or
// insecure host-key option. The operator owns the host/Connector mapping.
func Fetch(ctx context.Context, cfg SSHConfig, request Request) (Evidence, string, error) {
	if cfg.User == "" || !regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(cfg.HostAlias) || !regexp.MustCompile(`^/[A-Za-z0-9/._-]+$`).MatchString(cfg.Command) {
		return Evidence{}, "", ErrUnsafe
	}
	callback, err := knownhosts.New(cfg.KnownHosts)
	if err != nil {
		return Evidence{}, "", ErrUnsafe
	}
	key, err := os.ReadFile(cfg.PrivateKey)
	if err != nil || len(key) > 16384 {
		return Evidence{}, "", ErrUnsafe
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return Evidence{}, "", ErrUnsafe
	}
	fingerprint := ""
	config := &ssh.ClientConfig{User: cfg.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: func(_ string, remote net.Addr, key ssh.PublicKey) error {
		if err := callback(net.JoinHostPort(cfg.HostAlias, "22"), remote, key); err != nil {
			return err
		}
		fingerprint = ssh.FingerprintSHA256(key)
		return nil
	}}
	conn, err := (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, "tcp", cfg.Address)
	if err != nil {
		return Evidence{}, "", ErrUnsafe
	}
	defer conn.Close()
	deadline := time.Now().Add(time.Minute)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	clientConn, channels, requests, err := ssh.NewClientConn(conn, cfg.Address, config)
	if err != nil {
		return Evidence{}, "", ErrUnsafe
	}
	client := ssh.NewClient(clientConn, channels, requests)
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return Evidence{}, "", ErrUnsafe
	}
	defer session.Close()
	input, err := json.Marshal(request)
	if err != nil {
		return Evidence{}, "", ErrUnsafe
	}
	session.Stdin = bytes.NewReader(input)
	output := &boundedOutput{}
	session.Stdout, session.Stderr = output, io.Discard
	if err := session.Run(cfg.Command); err != nil {
		return Evidence{}, "", ErrUnsafe
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	decoder.DisallowUnknownFields()
	var evidence Evidence
	if decoder.Decode(&evidence) != nil || decoder.Decode(new(any)) != io.EOF {
		return Evidence{}, "", ErrUnsafe
	}
	return evidence, fingerprint, nil
}

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 65536 {
		return 0, ErrUnsafe
	}
	return b.Buffer.Write(p)
}
