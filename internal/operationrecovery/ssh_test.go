package operationrecovery

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPinnedSSHFetchRefusesWrongHostAndBoundsEvidence(t *testing.T) {
	for _, scenario := range []string{"valid", "wrong host", "large", "unknown field", "command injection"} {
		t.Run(scenario, func(t *testing.T) {
			_, private, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			signer, _ := ssh.NewSignerFromKey(private)
			serverCfg := &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil }}
			serverCfg.AddHostKey(signer)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				_, channels, requests, err := ssh.NewServerConn(conn, serverCfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(requests)
				for incoming := range channels {
					ch, requests, err := incoming.Accept()
					if err != nil {
						return
					}
					defer ch.Close()
					for req := range requests {
						if req.Type != "exec" {
							_ = req.Reply(false, nil)
							continue
						}
						_ = req.Reply(true, nil)
						var input Request
						_ = json.NewDecoder(ch).Decode(&input)
						if scenario == "large" {
							_, _ = ch.Write([]byte(strings.Repeat("x", 65537)))
						} else if scenario == "unknown field" {
							_, _ = ch.Write([]byte(`{"unknown":"field"}`))
						} else {
							_, _, proof := proofFixture()
							proof.Request = input
							_ = json.NewEncoder(ch).Encode(proof)
						}
						_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
						return
					}
				}
			}()
			base := t.TempDir()
			block, _ := ssh.MarshalPrivateKey(private, "")
			keyFile := filepath.Join(base, "key")
			_ = os.WriteFile(keyFile, pem.EncodeToMemory(block), 0600)
			pin := signer.PublicKey()
			if scenario == "wrong host" {
				_, other, _ := ed25519.GenerateKey(rand.Reader)
				otherSigner, _ := ssh.NewSignerFromKey(other)
				pin = otherSigner.PublicKey()
			}
			hosts := filepath.Join(base, "hosts")
			_ = os.WriteFile(hosts, []byte(knownhosts.Line([]string{"fixture"}, pin)+"\n"), 0600)
			cfg := SSHConfig{Address: listener.Addr().String(), User: "operator", HostAlias: "fixture", KnownHosts: hosts, PrivateKey: keyFile, Command: "/trusted/export"}
			if scenario == "command injection" {
				cfg.Command = "/trusted/export;echo injected"
			}
			_, req, _ := proofFixture()
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			proof, fingerprint, err := Fetch(ctx, cfg, req)
			if (err == nil) != (scenario == "valid") {
				t.Fatalf("scenario %s error=%v", scenario, err)
			}
			if err == nil && (proof.Request != req || fingerprint != ssh.FingerprintSHA256(signer.PublicKey())) {
				t.Fatal("pinned identity/request changed")
			}
			_ = listener.Close()
			<-done
		})
	}
}
