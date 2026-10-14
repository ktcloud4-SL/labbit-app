package openstackprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
)

func TestVMAccessProviderFailuresAndTargetMismatchControlCodes(t *testing.T) {
	for _, kind := range []string{"nova-503", "neutron-503", "nova-401", "neutron-401", "nova-json", "neutron-json", "missing-server", "other-lab", "stale-generation", "wrong-vm", "wrong-project", "wrong-port", "building", "no-ip", "unconfigured", "canceled", "deadline"} {
		t.Run(kind, func(t *testing.T) {
			const raw = "private-provider-response-do-not-expose"
			server := servers.Server{ID: "server-1", TenantID: "project-1", Status: "ACTIVE", Name: provisionBaseName("lab-1", 1) + "-workspace", Metadata: map[string]string{"labbit_lab_instance_id": "lab-1", "labbit_generation": "1", "labbit_operation_id": "op-1", "labbit_vm_key": "workspace"}}
			port := ports.Port{ID: "port-1", DeviceID: "server-1", DeviceOwner: "compute:nova", TenantID: "project-1", NetworkID: "management", FixedIPs: []ports.IP{{IPAddress: "192.0.2.10"}}}
			invalid := false
			switch kind {
			case "missing-server":
				invalid = true
			case "other-lab":
				server.Metadata["labbit_lab_instance_id"] = "lab-2"
				invalid = true
			case "stale-generation":
				server.Metadata["labbit_generation"] = "2"
				invalid = true
			case "wrong-vm":
				server.Metadata["labbit_vm_key"] = "worker"
				invalid = true
			case "wrong-project":
				server.TenantID = "project-2"
				invalid = true
			case "wrong-port":
				port.DeviceID = "other-server"
				invalid = true
			case "building":
				server.Status = "BUILD"
			case "no-ip":
				port.FixedIPs = nil
			}
			a := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				isNova := r.URL.Path == "/compute/v2/servers/server-1"
				isNeutron := r.URL.Path == "/network/v2.0/ports"
				if !isNova && !isNeutron {
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(500)
					return
				}
				if (isNova && kind == "nova-503") || (isNeutron && kind == "neutron-503") {
					http.Error(w, raw, 503)
					return
				}
				if (isNova && kind == "nova-401") || (isNeutron && kind == "neutron-401") {
					http.Error(w, raw, 401)
					return
				}
				if (isNova && kind == "nova-json") || (isNeutron && kind == "neutron-json") {
					_, _ = w.Write([]byte(raw))
					return
				}
				if isNova && kind == "missing-server" {
					http.Error(w, raw, 404)
					return
				}
				if isNova {
					writeJSON(t, w, 200, map[string]any{"server": server})
				} else {
					writeJSON(t, w, 200, map[string]any{"ports": []ports.Port{port}})
				}
			}))
			a.provision.ProjectID, a.provision.ManagementNetworkID = "project-1", "management"
			if kind == "unconfigured" {
				a.provision.ManagementNetworkID = ""
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "canceled" {
				cancel()
			}
			if kind == "deadline" {
				var expired context.CancelFunc
				ctx, expired = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer expired()
			}
			manager := vmaccess.NewManager(a, vmaccess.Config{})
			defer manager.Close()
			target := vmaccess.Target{LabInstanceID: "lab-1", Generation: 1, VMKey: "workspace", ServerID: "server-1"}
			_, err := manager.OpenSession(ctx, target)
			wantError, wantCode := vmaccess.ErrUnavailable, protocol.TerminalErrAttachFailed
			if invalid {
				wantError, wantCode = vmaccess.ErrInvalidTarget, protocol.TerminalErrInvalidSession
			}
			if !errors.Is(err, wantError) || (!invalid && errors.Is(err, vmaccess.ErrInvalidTarget)) {
				t.Fatalf("Manager classification=%v want %v", err, wantError)
			}
			if strings.Contains(err.Error(), raw) {
				t.Fatal("Manager exposed raw provider detail")
			}
			if kind == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation classification lost")
			}
			if kind == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("deadline classification lost")
			}
			var sent []any
			h := wss.NewHandler(a, wss.SendMessageFunc(func(_ context.Context, message any) error { sent = append(sent, message); return nil }))
			terminals := terminal.NewSessionManager(time.Minute, nil)
			defer terminals.CloseAll(protocol.TerminalReasonSessionClosed)
			h.SetTerminalManager(terminals, nil, terminal.DataWSSClientConfig{EndpointURL: "ws://127.0.0.1:1"})
			h.SetVMPTYFactory(func(ctx context.Context, target vmaccess.Target, cols, rows int) (terminal.PTYChannel, error) {
				return terminal.NewManagedSSHPTY(ctx, manager, target, cols, rows)
			})
			request := protocol.TerminalOpenMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeTerminalOpen, MessageID: "open-1", SentAt: time.Now().UTC(), TerminalSessionID: "session-1", LabInstanceID: "lab-1", Generation: 1}, Payload: protocol.TerminalOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server-1", Cols: 80, Rows: 24}}
			data, _ := json.Marshal(request)
			if err := h.HandleMessage(ctx, data); err == nil {
				t.Fatal("failure accepted")
			}
			if len(sent) != 1 {
				t.Fatalf("responses=%d", len(sent))
			}
			wire, err := json.Marshal(sent[0])
			if err != nil {
				t.Fatal(err)
			}
			var result protocol.TerminalOpenResultMessage
			if err := json.Unmarshal(wire, &result); err != nil {
				t.Fatal(err)
			}
			if result.Payload.Outcome != protocol.OutcomeFailed || result.Payload.Error == nil || result.Payload.Error.Code != wantCode {
				t.Fatalf("Control response=%+v want %s", result.Payload, wantCode)
			}
			if strings.Contains(string(wire), raw) || strings.Contains(string(wire), "192.0.2.10") {
				t.Fatal("raw provider detail escaped")
			}
		})
	}
}
