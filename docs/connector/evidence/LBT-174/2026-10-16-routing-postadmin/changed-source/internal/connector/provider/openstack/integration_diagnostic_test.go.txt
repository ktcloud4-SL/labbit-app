package openstackprovider

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// A diagnostic phase owns a dedicated connection. Closing only a session can
// leave Run waiting for exit status or output. Cancellation closes the entire
// owned transport and Run synchronously joins its output workers before return.
// Remote scripts still need their own bounded commands/finally cleanup; a lost
// SSH connection alone never proves that a remote process or route was removed.
func integrationDiagnosticSSH(ctx context.Context, client *ssh.Client, command string, output io.Writer) error {
	defer client.Close()
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stop()
	if err := ctx.Err(); err != nil {
		return err
	}
	session, err := client.NewSession()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	defer session.Close()
	writer := &integrationDiagnosticWriter{Writer: output}
	session.Stdout = writer
	session.Stderr = writer
	err = session.Run(command)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

type integrationDiagnosticWriter struct {
	sync.Mutex
	io.Writer
}

func (w *integrationDiagnosticWriter) Write(p []byte) (int, error) {
	w.Lock()
	defer w.Unlock()
	return w.Writer.Write(p)
}

func TestIntegrationDiagnosticCancellationJoinsExecAndClosesOwnedTransport(t *testing.T) {
	for i := 0; i < 10; i++ {
		t.Run("cancellation", func(t *testing.T) {
			started := make(chan struct{})
			address, peerDone := stalledCommandSSHServer(t, started)
			a := sshCancellationAdapter(t)
			key, err := os.ReadFile(a.provision.SSHPrivateKeyFile)
			if err != nil {
				t.Fatal(err)
			}
			signer, err := ssh.ParsePrivateKey(key)
			if err != nil {
				t.Fatal(err)
			}
			callback, err := trustOnFirstUseCallback(a.provision.SSHKnownHostsFile, "diagnostic-test:22")
			if err != nil {
				t.Fatal(err)
			}
			client, err := ssh.Dial("tcp", address, &ssh.ClientConfig{User: a.provision.SSHUsername, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: callback, Timeout: 3 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- integrationDiagnosticSSH(ctx, client, "held-diagnostic", io.Discard) }()
			awaitSSHTestSignal(t, started, "diagnostic exec")
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel result=%v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("Run not joined after cancellation")
			}
			select {
			case <-peerDone:
			case <-time.After(time.Second):
				t.Fatal("owned SSH transport remained open")
			}
		})
	}
}
