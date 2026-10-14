package app

import (
	"context"
	"errors"
	"testing"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
)

func TestFileCapabilityRequiresCompleteLazyRuntimeAssembly(t *testing.T) {
	t.Setenv("LABBIT_ENVIRONMENT", "development")
	t.Setenv("LABBIT_WORKSPACE_ROOT", "")
	t.Setenv("LABBIT_WORKSPACE_SFTP_HELPER", "")
	var calls int
	p := newLazyProvider("provider", func(context.Context) (runtimeProvider, error) { calls++; return nil, errors.New("unavailable fixture") })
	for _, enabled := range []bool{false, true} {
		if enabled {
			t.Setenv("LABBIT_WORKSPACE_ROOT", "/var/lib/labbit-workspaces/workspace")
			t.Setenv("LABBIT_WORKSPACE_SFTP_HELPER", "/usr/local/libexec/labbit-workspace-sftp")
		}
		cfg := &wss.Config{BaseURL: "https://saas.example.com", Credential: "test-credential", RuntimeID: "runtime", Capabilities: []string{protocol.CapabilityFileV1, protocol.CapabilityFileV1, "terminal"}}
		a, err := buildConnector(p, &recordSender{}, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if a.VMAccess != nil {
			defer a.VMAccess.Close()
		}
		if a.FileTransport != nil {
			defer a.FileTransport.Shutdown(context.Background())
		}
		count := 0
		for _, cap := range cfg.Capabilities {
			if cap == protocol.CapabilityFileV1 {
				count++
			}
		}
		if enabled && (count != 1 || a.FileTransport == nil || a.WorkspaceFiles == nil) || !enabled && (count != 0 || a.FileTransport != nil) || calls != 0 {
			t.Fatal("HELLO capability drifted from lazy File assembly")
		}
	}
	for _, base := range []string{"http://remote.example.com", "https://user:secret@example.com", "https://example.com?token=secret"} {
		cfg := &wss.Config{BaseURL: base, Credential: "test-credential", RuntimeID: "runtime"}
		if _, err := buildConnector(p, &recordSender{}, cfg); err == nil {
			t.Fatal("unsafe File endpoint accepted")
		}
	}
}
