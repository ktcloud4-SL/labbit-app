// Explicit real-cloud acceptance harness. Runs the production Connector process
// on the KT host and exercises its complete TLS Control supervisor path.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

type ledger struct {
	LabID     string                            `json:"labInstanceId"`
	Stage     string                            `json:"stage"`
	Resources []protocol.ProviderResourceResult `json:"resources"`
	Hello     bool                              `json:"hello"`
	Heartbeat bool                              `json:"heartbeat"`
	Passed    bool                              `json:"passed"`
}

var journal ledger
var messages = make(chan []byte, 256)
var service *mock.MockSaaS
var journalPath string

func save(stage string) error {
	journal.Stage = stage
	b, e := json.MarshalIndent(journal, "", "  ")
	if e != nil {
		return errors.New("safe ledger encode failed")
	}
	if os.WriteFile(journalPath, append(b, '\n'), 0600) != nil {
		return errors.New("safe ledger write failed")
	}
	fmt.Println("Control evidence:", stage)
	return nil
}
func merge(items []protocol.ProviderResourceResult) {
	for _, item := range items {
		found := false
		for i, r := range journal.Resources {
			if r.ResourceType == item.ResourceType && r.ProviderID == item.ProviderID {
				journal.Resources[i] = item
				found = true
				break
			}
		}
		if !found {
			journal.Resources = append(journal.Resources, item)
		}
	}
}
func envelope(kind, label string, generation int64) protocol.BaseEnvelope {
	return protocol.BaseEnvelope{Type: kind, MessageID: label + "-message", SentAt: time.Now().UTC(), RequestID: label + "-request", OperationID: label + "-operation", LabInstanceID: journal.LabID, Generation: generation}
}
func await(ctx context.Context, kind, reply string) ([]byte, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, errors.New("Control response timeout")
		case b := <-messages:
			var env protocol.BaseEnvelope
			if json.Unmarshal(b, &env) != nil {
				return nil, errors.New("Control response decode failed")
			}
			if env.Type == protocol.MessageTypeHeartbeat {
				journal.Heartbeat = true
				continue
			}
			if env.Type == "ERROR" {
				return nil, errors.New("Connector returned a protocol error")
			}
			if env.Type == kind && (reply == "" || env.ReplyToMessageID == reply) {
				return b, nil
			}
		}
	}
}
func query(ctx context.Context, kind string) error {
	command := protocol.ProviderRequestMessage{BaseEnvelope: envelope(protocol.MessageTypeProviderRequest, "query-"+kind, 1), Payload: protocol.ProviderRequestPayload{RequestType: kind, ProviderConnectionID: os.Getenv("LABBIT_PROVIDER_CONNECTION_ID")}}
	if service.SendRaw(command) != nil {
		return errors.New("Provider request send failed")
	}
	b, e := await(ctx, protocol.MessageTypeProviderResponse, command.MessageID)
	if e != nil {
		return e
	}
	var result protocol.ProviderResponseMessage
	if json.Unmarshal(b, &result) != nil || result.Payload.Outcome != "SUCCEEDED" {
		return errors.New("Provider query did not succeed")
	}
	fmt.Printf("Production Provider query %s PASS; items=%d\n", kind, len(result.Payload.Items))
	return save("QUERY_" + kind + "_PASS")
}
func mutate(ctx context.Context, kind, label string, generation int64, snapshot *protocol.CreationSnapshot, refs []protocol.ProviderResourceRef) ([]protocol.ProviderResourceResult, error) {
	command := protocol.OperationCommandMessage{BaseEnvelope: envelope(protocol.MessageTypeOperationCommand, label, generation), Payload: protocol.OperationCommandPayload{MutationType: kind, CreationSnapshot: snapshot, ProviderResources: refs}}
	if service.SendRaw(command) != nil {
		return nil, errors.New("operation command send failed")
	}
	b, e := await(ctx, protocol.MessageTypeOperationAck, command.MessageID)
	if e != nil {
		return nil, e
	}
	var ack protocol.OperationAckMessage
	if json.Unmarshal(b, &ack) != nil || !ack.Payload.Accepted {
		return nil, errors.New("operation ACK was not accepted")
	}
	b, e = await(ctx, protocol.MessageTypeOperationResult, command.MessageID)
	if e != nil {
		return nil, e
	}
	var result protocol.OperationResultMessage
	if json.Unmarshal(b, &result) != nil {
		return nil, errors.New("operation result decode failed")
	}
	merge(result.Payload.ProviderResources)
	if save(label+"_"+result.Payload.Outcome) != nil {
		return nil, errors.New("result ledger save failed")
	}
	fmt.Printf("Actual WSS %s(g%d): outcome=%s resources=%d\n", kind, generation, result.Payload.Outcome, len(result.Payload.ProviderResources))
	if result.Payload.Error != nil {
		fmt.Println("Safe Provider error:", result.Payload.Error.Code, result.Payload.Error.Message)
	}
	if result.Payload.Outcome != "SUCCEEDED" {
		return result.Payload.ProviderResources, errors.New("operation did not succeed; confirmed resources retained for cleanup")
	}
	return result.Payload.ProviderResources, nil
}
func refs(items []protocol.ProviderResourceResult, generation int64) []protocol.ProviderResourceRef {
	out := make([]protocol.ProviderResourceRef, 0)
	for _, item := range items {
		if item.Generation == generation && item.ObservedState != "DELETED" {
			out = append(out, item.ProviderResourceRef)
		}
	}
	return out
}
func reconcile(ctx context.Context, label string, generation int64, known []protocol.ProviderResourceRef) ([]protocol.ResourceObservation, error) {
	discover := true
	command := protocol.ReconcileRequestMessage{BaseEnvelope: envelope(protocol.MessageTypeReconcileRequest, label, generation), Payload: protocol.ReconcileRequestPayload{KnownResources: known, DiscoverCandidates: &discover}}
	if service.SendRaw(command) != nil {
		return nil, errors.New("reconcile send failed")
	}
	b, e := await(ctx, protocol.MessageTypeReconcileResult, command.MessageID)
	if e != nil {
		return nil, e
	}
	var result protocol.ReconcileResultMessage
	if json.Unmarshal(b, &result) != nil || result.Payload.Error != nil {
		return nil, errors.New("reconcile response failed")
	}
	return result.Payload.Observations, nil
}
func run() error {
	if os.Getenv("LABBIT_KTCLOUD_D1_CONTROL_TEST") != "1" {
		return errors.New("explicit cloud Control gate is required")
	}
	workspace := os.Getenv("LBT145_CONTROL_DIRECTORY")
	if !filepath.IsAbs(workspace) {
		return errors.New("absolute local Control directory is required")
	}
	journalPath = filepath.Join(workspace, "control-ledger.json")
	recovery := os.Getenv("LBT145_CONTROL_RECOVERY") == "1"
	if _, e := os.Stat(journalPath); e == nil {
		if !recovery {
			return errors.New("prior fixture ledger exists; reconcile/cleanup before another run")
		}
		b, e := os.ReadFile(journalPath)
		if e != nil || json.Unmarshal(b, &journal) != nil {
			return errors.New("prior fixture ledger unavailable")
		}
	}
	if !recovery {
		r := make([]byte, 8)
		if _, e := rand.Read(r); e != nil {
			return errors.New("fixture identifier generation failed")
		}
		journal.LabID = "lbt145-control-" + hex.EncodeToString(r)
		journal.Resources = []protocol.ProviderResourceResult{}
		if e := save("CONTROL_FIXTURE_REGISTERED"); e != nil {
			return e
		}
	}
	journal.Hello = false
	journal.Heartbeat = false
	credential := make([]byte, 32)
	if _, e := rand.Read(credential); e != nil {
		return errors.New("mock credential generation failed")
	}
	token := hex.EncodeToString(credential)
	credentialPath := filepath.Join(workspace, "mock-credential")
	if os.WriteFile(credentialPath, []byte(token), 0600) != nil {
		return errors.New("mock credential save failed")
	}
	service = mock.NewMockSaaS(token)
	handler := service.Server.Config.Handler
	service.Server.Close()
	service.Server = httptest.NewTLSServer(handler)
	defer service.Close()
	certificatePath := filepath.Join(workspace, "mock-ca.pem")
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: service.Server.Certificate().Raw})
	if os.WriteFile(certificatePath, certificate, 0600) != nil {
		return errors.New("mock TLS certificate save failed")
	}
	service.OnMsgReceived = func(raw []byte) {
		select {
		case messages <- append([]byte(nil), raw...):
		default:
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, filepath.Join(workspace, "labbit-connector"))
	command.Env = append(os.Environ(), "LABBIT_ENVIRONMENT=production", "LABBIT_CONNECTOR_ID=lbt145-ktcloud-fixture", "LABBIT_SAAS_BASE_URL="+service.Server.URL, "LABBIT_CONNECTOR_CREDENTIAL_FILE="+credentialPath, "SSL_CERT_FILE="+certificatePath)
	logName := "connector.log"
	if recovery {
		logName = fmt.Sprintf("connector-recovery-%d.log", time.Now().UnixNano())
	}
	logFile, e := os.OpenFile(filepath.Join(workspace, logName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return errors.New("connector log path unavailable")
	}
	defer logFile.Close()
	command.Stdout = logFile
	command.Stderr = logFile
	if command.Start() != nil {
		return errors.New("production Connector launch failed")
	}
	defer func() {
		_ = command.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = command.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
		}
	}()
	helloContext, stopHello := context.WithTimeout(ctx, 45*time.Second)
	defer stopHello()
	if _, e = await(helloContext, protocol.MessageTypeHello, ""); e != nil {
		return e
	}
	journal.Hello = true
	for !journal.Heartbeat {
		select {
		case <-helloContext.Done():
			return errors.New("heartbeat not observed")
		case b := <-messages:
			var env protocol.BaseEnvelope
			_ = json.Unmarshal(b, &env)
			if env.Type == protocol.MessageTypeHeartbeat {
				journal.Heartbeat = true
			}
		}
	}
	if e = save("HELLO_HELLO_ACK_HEARTBEAT_PASS"); e != nil {
		return e
	}
	for _, kind := range []string{protocol.ProviderRequestValidateConnection, protocol.ProviderRequestListImages, protocol.ProviderRequestListFlavors} {
		if e = query(ctx, kind); e != nil {
			return e
		}
	}
	if recovery {
		known := refs(journal.Resources, 1)
		known = append(known, refs(journal.Resources, 2)...)
		readOnly := os.Getenv("LBT145_CONTROL_RECOVERY_READ_ONLY") == "1"
		if readOnly {
			known = make([]protocol.ProviderResourceRef, 0, len(journal.Resources))
			for _, r := range journal.Resources {
				known = append(known, r.ProviderResourceRef)
			}
		}
		observations, e := reconcile(ctx, "recover-observe-before-any-mutation", 2, known)
		if e != nil {
			return e
		}
		present := []protocol.ProviderResourceRef{}
		for _, o := range observations {
			ref := protocol.ProviderResourceRef{ResourceType: o.ResourceType, ProviderID: o.ProviderID, Generation: o.Generation, LogicalName: o.LogicalName}
			if o.Source != "KNOWN_RESOURCE" {
				return errors.New("recovery found an unowned candidate; manual review required")
			}
			if o.Exists {
				present = append(present, ref)
			} else if o.ObservedState == "ABSENT" {
				merge([]protocol.ProviderResourceResult{{ProviderResourceRef: ref, ObservedState: "DELETED"}})
			} else {
				return errors.New("recovery observation uncertain")
			}
		}
		if len(observations) != len(known) {
			return errors.New("recovery inventory incomplete")
		}
		if e = save("RECOVERY_RECONCILE_BEFORE_MUTATION_PASS"); e != nil {
			return e
		}
		if readOnly {
			fmt.Printf("Actual WSS read-only Reconcile: known=%d present=%d absent=%d; no mutation requested\n", len(known), len(present), len(known)-len(present))
			return save("CURRENT_CONNECTOR_READ_ONLY_RECONCILE_COMPLETE_FULL_ACCEPTANCE_PENDING")
		}
		if _, e = mutate(ctx, protocol.MutationTypeCleanup, "recovery-cleanup", 2, nil, present); e != nil {
			return e
		}
		observations, e = reconcile(ctx, "recover-confirm-absence", 2, known)
		if e != nil {
			return e
		}
		for _, o := range observations {
			if o.Exists || o.ObservedState != "ABSENT" {
				return errors.New("recovery cleanup absence not confirmed")
			}
		}
		return save("RECOVERY_KNOWN_RESOURCE_CLEANUP_ABSENCE_CONFIRMED")
	}
	var snapshot protocol.CreationSnapshot
	b, e := os.ReadFile(filepath.Join(workspace, "snapshot.json"))
	if e != nil || json.Unmarshal(b, &snapshot) != nil {
		return errors.New("immutable fixture Snapshot unavailable")
	}
	content := "#!/bin/sh\nset -eu\nprintf 'lbt145-startup-ready\\n' > /tmp/lbt145-startup-ready\n"
	digest := sha256.Sum256([]byte(content))
	snapshot.StartupScript = &protocol.StartupScriptSnapshot{Content: content, SHA256: hex.EncodeToString(digest[:])}
	defer func() {
		owned := refs(journal.Resources, 1)
		owned = append(owned, refs(journal.Resources, 2)...)
		if len(owned) == 0 {
			return
		}
		cleanupContext, stop := context.WithTimeout(context.Background(), 8*time.Minute)
		defer stop()
		_, err := mutate(cleanupContext, protocol.MutationTypeCleanup, "teardown", 2, nil, owned)
		if err != nil {
			fmt.Println("CONTROL TEARDOWN NEEDS ATTENTION; safe tracked-ID ledger retained")
		}
	}()
	first, e := mutate(ctx, protocol.MutationTypeProvision, "provision", 1, &snapshot, nil)
	if e != nil {
		return e
	}
	firstRefs := refs(first, 1)
	if len(firstRefs) != 5 {
		return errors.New("Provision resource set differs from KT profile")
	}
	if e = proveManagementSSH(ctx, firstRefs, 1); e != nil {
		return e
	}
	observations, e := reconcile(ctx, "reconcile-generation1", 1, firstRefs)
	if e != nil {
		return e
	}
	for _, o := range observations {
		if !o.Exists || o.Source != "KNOWN_RESOURCE" {
			return errors.New("Provision resource reality mismatch")
		}
	}
	if len(observations) != 5 {
		return errors.New("Provision reconcile cardinality mismatch")
	}
	if e = save("GENERATION1_RECONCILE_PASS"); e != nil {
		return e
	}
	second, e := mutate(ctx, protocol.MutationTypeReset, "reset", 2, &snapshot, firstRefs)
	if e != nil {
		return e
	}
	secondRefs := refs(second, 2)
	if len(secondRefs) != 5 {
		return errors.New("Reset replacement set is incomplete")
	}
	if e = proveManagementSSH(ctx, secondRefs, 2); e != nil {
		return e
	}
	oldServer, newServer := "", ""
	for _, item := range firstRefs {
		if item.ResourceType == "SERVER" {
			oldServer = item.ProviderID
		}
	}
	for _, item := range secondRefs {
		if item.ResourceType == "SERVER" {
			newServer = item.ProviderID
		}
	}
	if oldServer == "" || newServer == "" || oldServer == newServer {
		return errors.New("Reset did not replace the actual VM")
	}
	all := append(append([]protocol.ProviderResourceRef{}, firstRefs...), secondRefs...)
	observations, e = reconcile(ctx, "reconcile-after-reset", 2, all)
	if e != nil {
		return e
	}
	missing, present := 0, 0
	for _, o := range observations {
		if o.Source != "KNOWN_RESOURCE" {
			return errors.New("unexpected candidate after Reset")
		}
		if o.Generation == 1 && !o.Exists && o.ObservedState == "ABSENT" {
			missing++
		}
		if o.Generation == 2 && o.Exists {
			present++
		}
	}
	if missing != 5 || present != 5 {
		return errors.New("Reset known Missing boundary differs from cloud reality")
	}
	if e = save("RESET_TARGET_CHANGED_OLD_ABSENT_NEW_PRESENT_PASS"); e != nil {
		return e
	}
	subset := []protocol.ProviderResourceRef{}
	for _, item := range secondRefs {
		if item.ResourceType != "SERVER" {
			subset = append(subset, item)
		}
	}
	observations, e = reconcile(ctx, "candidate-check", 2, subset)
	if e != nil {
		return e
	}
	candidate := false
	for _, o := range observations {
		if o.ResourceType == "SERVER" && o.ProviderID == newServer && o.Source == "DISCOVERED_CANDIDATE" {
			candidate = true
		}
	}
	if !candidate {
		return errors.New("untracked server was not a discovered candidate")
	}
	if _, e = mutate(ctx, protocol.MutationTypeCleanup, "empty-cleanup", 2, nil, []protocol.ProviderResourceRef{}); e != nil {
		return e
	}
	observations, e = reconcile(ctx, "candidate-remains", 2, secondRefs)
	if e != nil {
		return e
	}
	for _, o := range observations {
		if !o.Exists {
			return errors.New("empty Cleanup removed a discovered candidate")
		}
	}
	if e = save("CANDIDATE_NOT_OWNED_OR_AUTODELETED_PASS"); e != nil {
		return e
	}
	if _, e = mutate(ctx, protocol.MutationTypeCleanup, "cleanup", 2, nil, secondRefs); e != nil {
		return e
	}
	observations, e = reconcile(ctx, "final-known-absence", 2, all)
	if e != nil {
		return e
	}
	for _, o := range observations {
		if o.Exists || o.Source != "KNOWN_RESOURCE" || o.ObservedState != "ABSENT" {
			return errors.New("final owned resource absence not confirmed")
		}
	}
	if len(observations) != 10 {
		return errors.New("final generation resources missing from reconciliation")
	}
	journal.Passed = true
	return save("REAL_PRODUCTION_CONNECTOR_WSS_LIFECYCLE_PASS_OWNED_RESIDUAL_0")
}
func main() {
	if e := run(); e != nil {
		fmt.Println("Control acceptance failed:", e.Error())
		os.Exit(1)
	}
}
