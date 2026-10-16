package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/preview"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	openstackprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider/openstack"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Fixture-only selection. Preview Data WSS and Browser acceptance are separate.
func validatePreviewScope(enabled, op02 bool, generations int, recovery bool) error {
	if enabled && (op02 || generations != 2 || recovery) {
		return errors.New("Preview smoke requires a fresh exclusive two-generation fixture")
	}
	return nil
}

func requirePreviewCapability(raw []byte) error {
	var hello struct {
		Payload struct {
			Capabilities []string `json:"capabilities"`
		} `json:"payload"`
	}
	if json.Unmarshal(raw, &hello) != nil {
		return errors.New("Preview capability HELLO malformed")
	}
	for _, capability := range hello.Payload.Capabilities {
		if capability == protocol.CapabilityPreviewV1 {
			return nil
		}
	}
	return errors.New("Production Connector did not advertise preview-v1")
}

// Uses the production SSHForwarder/VMAccess on the actual KT Connector host.
// Provider mutations remain exclusively in the existing journaled Control path.
func provePreviewForwarding(ctx context.Context, resources []protocol.ProviderResourceRef, generation int64, oldServer string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	adapter, err := openstackprovider.New(ctx, openstackprovider.ConfigFromEnvironment())
	if err != nil {
		return errors.New("Preview fixture Provider initialization failed")
	}
	access := vmaccess.NewManager(adapter, vmaccess.Config{})
	defer access.Close()
	stopClose := context.AfterFunc(ctx, func() { _ = access.Close() })
	defer stopClose()
	target := vmaccess.Target{LabInstanceID: journal.LabID, Generation: generation, VMKey: "workspace", ServerID: serverRef(resources)}
	forwarder := &preview.SSHForwarder{Access: access, Validator: adapter}
	server, err := access.OpenSession(ctx, target)
	if err != nil {
		return errors.New("Preview HTTP fixture pinned SSH unavailable")
	}
	defer server.Close()
	var stdout io.Reader
	err = server.Setup(ctx, func(raw *ssh.Session) error {
		var err error
		stdout, err = raw.StdoutPipe()
		if err != nil {
			return err
		}
		raw.Stderr = io.Discard
		return raw.Start("exec python3 -u -c 'from http.server import BaseHTTPRequestHandler, HTTPServer; exec(\"class Handler(BaseHTTPRequestHandler):\\n def do_GET(self):\\n  body=b\\\"LBT173-HTTP-OK\\\"; self.send_response(200); self.send_header(\\\"Content-Length\\\",str(len(body))); self.end_headers(); self.wfile.write(body)\\n def log_message(self,*args): pass\"); server=HTTPServer((\"127.0.0.1\",3000),Handler); print(\"READY\",flush=True); server.serve_forever()'")
	})
	if err != nil {
		return errors.New("Preview HTTP fixture setup failed")
	}
	ready := make(chan bool, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		ready <- err == nil && line == "READY\n"
	}()
	select {
	case ok := <-ready:
		if !ok {
			return errors.New("Preview HTTP fixture did not start")
		}
	case <-ctx.Done():
		return errors.New("Preview HTTP fixture timed out")
	}
	pty, err := terminal.NewManagedSSHPTY(ctx, access, target, 80, 24)
	if err != nil {
		return errors.New("Preview sibling PTY setup failed")
	}
	defer pty.Close()
	file, err := access.OpenSession(ctx, target)
	if err != nil {
		return errors.New("Preview sibling SFTP SSH setup failed")
	}
	defer file.Close()
	var in io.WriteCloser
	var out io.Reader
	err = file.Setup(ctx, func(raw *ssh.Session) error {
		var err error
		in, err = raw.StdinPipe()
		if err != nil {
			return err
		}
		out, err = raw.StdoutPipe()
		if err != nil {
			return err
		}
		raw.Stderr = io.Discard
		return raw.RequestSubsystem("sftp")
	})
	if err != nil {
		return errors.New("Preview sibling SFTP subsystem failed")
	}
	client, err := sftp.NewClientPipe(out, in)
	if err != nil {
		return errors.New("Preview sibling SFTP initialization failed")
	}
	defer client.Close()
	identity, err := previewSSHIdentity(ctx, access, target)
	if err != nil {
		return err
	}
	for range 2 {
		conn, err := forwarder.DialTCP(ctx, target, 3000)
		if err != nil {
			return errors.New("KT SSH Preview forwarding failed")
		}
		err = checkPreviewHTTP(conn)
		_ = conn.Close()
		if err != nil {
			return err
		}
	}
	after, err := previewSSHIdentity(ctx, access, target)
	if err != nil || !bytes.Equal(identity, after) {
		return errors.New("Preview did not reuse sibling SSH transport")
	}
	if _, err := client.Stat("."); err != nil {
		return errors.New("Preview close broke sibling SFTP")
	}
	if _, err := pty.Write([]byte("printf 'LBT173-%s\\n' 'PTY-OK'\n")); err != nil {
		return errors.New("Preview close broke sibling PTY write")
	}
	marker := make(chan bool, 1)
	go func() {
		var body []byte
		buf := make([]byte, 1024)
		for len(body) < 32768 {
			n, err := pty.Read(buf)
			body = append(body, buf[:n]...)
			if bytes.Contains(body, []byte("LBT173-PTY-OK")) {
				marker <- true
				return
			}
			if err != nil {
				break
			}
		}
		marker <- false
	}()
	select {
	case ok := <-marker:
		if !ok {
			return errors.New("Preview sibling PTY marker missing; contents withheld")
		}
	case <-ctx.Done():
		return errors.New("Preview sibling PTY timed out; contents withheld")
	}
	for _, bad := range []vmaccess.Target{
		{LabInstanceID: "other", Generation: generation, VMKey: target.VMKey, ServerID: target.ServerID},
		{LabInstanceID: target.LabInstanceID, Generation: generation + 1, VMKey: target.VMKey, ServerID: target.ServerID},
		{LabInstanceID: target.LabInstanceID, Generation: generation, VMKey: "other", ServerID: target.ServerID},
		{LabInstanceID: target.LabInstanceID, Generation: generation, VMKey: target.VMKey, ServerID: "missing-server"},
	} {
		if _, err := forwarder.DialTCP(ctx, bad, 3000); !errors.Is(err, preview.ErrVMUnreachable) {
			return errors.New("KT Preview accepted wrong target")
		}
	}
	for _, port := range []int{-1, 0, 22, 65536} {
		if _, err := forwarder.DialTCP(ctx, target, port); !errors.Is(err, preview.ErrPortRejected) {
			return errors.New("KT Preview accepted unsafe port")
		}
	}
	if _, err := forwarder.DialTCP(ctx, target, 3001); !errors.Is(err, preview.ErrAppNotRunning) {
		return errors.New("KT Preview closed app port classification failed")
	}
	if generation == 2 {
		for _, stale := range []vmaccess.Target{
			{LabInstanceID: target.LabInstanceID, Generation: 1, VMKey: target.VMKey, ServerID: oldServer},
			{LabInstanceID: target.LabInstanceID, Generation: 1, VMKey: target.VMKey, ServerID: target.ServerID},
		} {
			if _, err := forwarder.DialTCP(ctx, stale, 3000); !errors.Is(err, preview.ErrVMUnreachable) {
				return errors.New("KT Preview accepted stale generation after Reset")
			}
		}
	}
	fmt.Printf("KT Preview g%d: pinned management SSH direct-tcpip loopback:3000 HTTP200/exact-body twice, shared transport, PTY/SFTP isolation, target/port/refused guards PASS; bodies withheld\n", generation)
	return save(fmt.Sprintf("PREVIEW_GENERATION%d_HTTP_SHARED_SSH_TARGET_PORT_ISOLATION_PASS", generation))
}

func previewSSHIdentity(ctx context.Context, access *vmaccess.Manager, target vmaccess.Target) ([]byte, error) {
	s, err := access.OpenSession(ctx, target)
	if err != nil {
		return nil, errors.New("Preview SSH transport probe unavailable")
	}
	defer s.Close()
	var output bytes.Buffer
	err = s.Setup(ctx, func(raw *ssh.Session) error {
		raw.Stdout = &output
		raw.Stderr = io.Discard
		return raw.Run("printf '%s' \"$SSH_CONNECTION\"")
	})
	if err != nil || output.Len() == 0 {
		return nil, errors.New("Preview SSH transport probe failed")
	}
	return output.Bytes(), nil
}

func checkPreviewHTTP(conn net.Conn) error {
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: fixture\r\nConnection: close\r\n\r\n"); err != nil {
		return errors.New("Preview HTTP write failed")
	}
	return validatePreviewHTTP(bufio.NewReader(conn))
}

func validatePreviewHTTP(reader *bufio.Reader) error {
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		return errors.New("Preview HTTP response malformed; contents withheld")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 128))
	if err != nil || response.StatusCode != http.StatusOK || !bytes.Equal(body, []byte("LBT173-HTTP-OK")) {
		return errors.New("Preview HTTP status/body mismatch; contents withheld")
	}
	return nil
}
