package valkeyowner

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func TestLocator_Lifecycle(t *testing.T) {
	locA, err := NewLocator(Config{
		ValkeyAddr: "127.0.0.1:6379",
		PodID:      "pod-a",
		PodAddr:    "127.0.0.1:8081",
	})
	if err != nil {
		t.Skipf("Valkey not available at 127.0.0.1:6379: %v", err)
	}
	defer locA.Close()

	locB, err := NewLocator(Config{
		ValkeyAddr: "127.0.0.1:6379",
		PodID:      "pod-b",
		PodAddr:    "127.0.0.1:8082",
	})
	if err != nil {
		t.Fatalf("failed to init locB: %v", err)
	}
	defer locB.Close()

	ctx := t.Context()
	connectorID := uuid.New()

	// 1. Initial Lookup should fail with ErrNoOwner
	_, err = locB.LookupConnector(ctx, connectorID)
	if !errors.Is(err, ErrNoOwner) {
		t.Fatalf("LookupConnector on unowned connector err = %v, want ErrNoOwner", err)
	}

	// 2. Pod A registers connector with epoch 2
	if _, err := locA.RegisterConnector(ctx, connectorID, 2, 3*time.Second); err != nil {
		t.Fatalf("RegisterConnector err = %v", err)
	}

	// 3. Pod B looks up connector: should point to Pod A
	owner, err := locB.LookupConnector(ctx, connectorID)
	if err != nil {
		t.Fatalf("LookupConnector err = %v", err)
	}
	if owner.PodID != "pod-a" || owner.PodAddr != "127.0.0.1:8081" || owner.Epoch != 2 {
		t.Fatalf("unexpected owner record: %+v", owner)
	}

	// 4. Stale registration: Pod B tries to register with lower epoch 1 -> should fail with ErrStaleEpoch
	_, err = locB.RegisterConnector(ctx, connectorID, 1, 3*time.Second)
	if !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("RegisterConnector with lower epoch err = %v, want ErrStaleEpoch", err)
	}

	// 4b. Equal epoch registration: Pod B tries to register with equal epoch 2 -> should also fail with ErrStaleEpoch
	_, err = locB.RegisterConnector(ctx, connectorID, 2, 3*time.Second)
	if !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("RegisterConnector with equal epoch err = %v, want ErrStaleEpoch", err)
	}

	// 5. Reconnect: Pod B registers with higher epoch 3 -> takes ownership
	if _, err := locB.RegisterConnector(ctx, connectorID, 3, 3*time.Second); err != nil {
		t.Fatalf("RegisterConnector with higher epoch err = %v", err)
	}

	owner2, err := locA.LookupConnector(ctx, connectorID)
	if err != nil {
		t.Fatalf("LookupConnector after epoch 3 err = %v", err)
	}
	if owner2.PodID != "pod-b" || owner2.Epoch != 3 {
		t.Fatalf("expected pod-b to own connector with epoch 3, got: %+v", owner2)
	}

	// 6. Old Pod A tries to refresh epoch 2 -> should fail with ErrStaleEpoch
	err = locA.RefreshConnector(ctx, connectorID, 2, 3*time.Second)
	if !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("stale refresh err = %v, want ErrStaleEpoch", err)
	}

	// 7. Pod B releases connector
	if err := locB.ReleaseConnector(ctx, connectorID, 3); err != nil {
		t.Fatalf("ReleaseConnector err = %v", err)
	}

	// 8. Lookup after release should return ErrNoOwner
	_, err = locA.LookupConnector(ctx, connectorID)
	if !errors.Is(err, ErrNoOwner) {
		t.Fatalf("Lookup after release err = %v, want ErrNoOwner", err)
	}
}

func TestLocator_AtomicGenerationAndOwnerLossCallback(t *testing.T) {
	locA, err := NewLocator(Config{
		ValkeyAddr: "127.0.0.1:6379",
		PodID:      "pod-a",
		PodAddr:    "127.0.0.1:8081",
	})
	if err != nil {
		t.Skipf("Valkey unavailable: %v", err)
	}
	defer locA.Close()

	locB, err := NewLocator(Config{
		ValkeyAddr: "127.0.0.1:6379",
		PodID:      "pod-b",
		PodAddr:    "127.0.0.1:8082",
	})
	if err != nil {
		t.Skipf("Valkey unavailable: %v", err)
	}
	defer locB.Close()

	ctx := context.Background()
	connectorID := uuid.New()

	lostA := make(chan struct{})
	onLostA := func() {
		select {
		case <-lostA:
		default:
			close(lostA)
		}
	}

	// 1. Pod A registers with epoch=0 -> atomic monotonic generation allocated via Valkey INCR
	epochA, err := locA.RegisterConnector(ctx, connectorID, 0, 2*time.Second, onLostA)
	if err != nil {
		t.Fatalf("RegisterConnector A err = %v", err)
	}
	if epochA <= 0 {
		t.Fatalf("expected epochA > 0, got %d", epochA)
	}

	// 2. Pod B registers with epoch=0 -> atomic monotonic generation must be strictly greater than epochA
	epochB, err := locB.RegisterConnector(ctx, connectorID, 0, 2*time.Second)
	if err != nil {
		t.Fatalf("RegisterConnector B err = %v", err)
	}
	if epochB <= epochA {
		t.Fatalf("expected epochB (%d) > epochA (%d)", epochB, epochA)
	}

	// 3. Pod A's lease renewal should detect STALE_EPOCH and invoke onLost callback
	select {
	case <-lostA:
		// Succeeded: onLost was invoked
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for onLost callback on Pod A after Pod B takeover")
	}

	// 4. Stale Pod A refresh must fail-closed with ErrStaleEpoch
	if err := locA.RefreshConnector(ctx, connectorID, epochA, 2*time.Second); !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("expected ErrStaleEpoch on stale refresh, got %v", err)
	}

	// 5. Stale Pod A release with old epoch must NOT release Pod B's mapping
	_ = locA.ReleaseConnector(ctx, connectorID, epochA)
	recAfterStaleRelease, err := locB.LookupConnector(ctx, connectorID)
	if err != nil || recAfterStaleRelease == nil {
		t.Fatalf("expected owner mapping to remain Pod B, err=%v, rec=%+v", err, recAfterStaleRelease)
	}
	if recAfterStaleRelease.PodID != "pod-b" || recAfterStaleRelease.Epoch != epochB {
		t.Fatalf("expected owner pod-b with epochB %d, got %+v", epochB, recAfterStaleRelease)
	}

	// Clean up Pod B
	_ = locB.ReleaseConnector(ctx, connectorID, epochB)
}

func TestLocator_TerminalOwner(t *testing.T) {
	loc, err := NewLocator(Config{
		ValkeyAddr: "127.0.0.1:6379",
		PodID:      "pod-a",
		PodAddr:    "127.0.0.1:8081",
	})
	if err != nil {
		t.Skipf("Valkey not available: %v", err)
	}
	defer loc.Close()

	ctx := t.Context()
	sessionID := "ts-" + uuid.NewString()
	connectorID := uuid.New()

	// Initial lookup should return ErrNoOwner
	_, err = loc.LookupTerminal(ctx, sessionID)
	if !errors.Is(err, ErrNoOwner) {
		t.Fatalf("LookupTerminal before register err = %v, want ErrNoOwner", err)
	}

	// Register terminal
	if err := loc.RegisterTerminal(ctx, sessionID, connectorID, 1, 10*time.Minute); err != nil {
		t.Fatalf("RegisterTerminal err = %v", err)
	}

	rec, err := loc.LookupTerminal(ctx, sessionID)
	if err != nil {
		t.Fatalf("LookupTerminal err = %v", err)
	}
	if rec.TerminalSessionID != sessionID || rec.PodID != "pod-a" || rec.PodAddr != "127.0.0.1:8081" {
		t.Fatalf("unexpected terminal owner record: %+v", rec)
	}

	// Release terminal
	if err := loc.ReleaseTerminal(ctx, sessionID); err != nil {
		t.Fatalf("ReleaseTerminal err = %v", err)
	}

	_, err = loc.LookupTerminal(ctx, sessionID)
	if !errors.Is(err, ErrNoOwner) {
		t.Fatalf("LookupTerminal after release err = %v, want ErrNoOwner", err)
	}
}

func TestLocator_TerminalOwnerHeartbeatRenewal(t *testing.T) {
	loc, err := NewLocator(Config{
		ValkeyAddr: "127.0.0.1:6379",
		PodID:      "pod-hb",
		PodAddr:    "127.0.0.1:8083",
	})
	if err != nil {
		t.Skipf("Valkey not available at 127.0.0.1:6379: %v", err)
	}
	defer loc.Close()

	ctx := t.Context()
	sessionID := uuid.New().String()
	connectorID := uuid.New()

	// Short TTL of 1 second (heartbeat interval is ~333ms)
	ttl := 1 * time.Second
	if err := loc.RegisterTerminal(ctx, sessionID, connectorID, 1, ttl); err != nil {
		t.Fatalf("RegisterTerminal err = %v", err)
	}

	// Wait 1.3s (> initial 1.0s TTL). Without heartbeat renewal, key would expire.
	time.Sleep(1300 * time.Millisecond)

	rec, err := loc.LookupTerminal(ctx, sessionID)
	if err != nil {
		t.Fatalf("LookupTerminal after initial TTL elapsed should succeed via heartbeat renewal, got err = %v", err)
	}
	if rec == nil || rec.TerminalSessionID != sessionID || rec.PodID != "pod-hb" {
		t.Fatalf("unexpected record after renewal: %+v", rec)
	}

	// Release terminal should stop heartbeat and remove record
	if err := loc.ReleaseTerminal(ctx, sessionID); err != nil {
		t.Fatalf("ReleaseTerminal err = %v", err)
	}

	_, err = loc.LookupTerminal(ctx, sessionID)
	if !errors.Is(err, ErrNoOwner) {
		t.Fatalf("LookupTerminal immediately after release err = %v, want ErrNoOwner", err)
	}

	// Wait 500ms to verify cancelled heartbeat does not re-create key
	time.Sleep(500 * time.Millisecond)
	_, err = loc.LookupTerminal(ctx, sessionID)
	if !errors.Is(err, ErrNoOwner) {
		t.Fatalf("LookupTerminal after heartbeat stopped err = %v, want ErrNoOwner", err)
	}
}

func TestConnectorKeyAndEpochKeyShareClusterHashSlot(t *testing.T) {
	// Verify standard Redis/Valkey CRC16 test vector: "123456789" -> 0x31C3 (12739), slot = 12739 % 16384 = 12739
	if got := HashSlot("{123456789}"); got != 12739 {
		t.Fatalf("HashSlot({123456789}) = %d, want 12739", got)
	}
	for i := 0; i < 100; i++ {
		id := uuid.New()
		ownerKey := ConnectorKey(id)
		epochKey := ConnectorEpochKey(id)
		ownerSlot := HashSlot(ownerKey)
		epochSlot := HashSlot(epochKey)
		if ownerSlot != epochSlot {
			t.Fatalf("CROSSSLOT mismatch for %s: ownerKey=%q (slot %d) != epochKey=%q (slot %d)",
				id, ownerKey, ownerSlot, epochKey, epochSlot)
		}
		termKey := TerminalKey(id.String())
		if HashSlot(termKey) != ownerSlot {
			t.Fatalf("expected TerminalKey(%s) to hash to same slot %d, got %d", id, ownerSlot, HashSlot(termKey))
		}
	}
}

func TestBuildClientOption_ClusterTLSAuthAndSecretRedaction(t *testing.T) {
	t.Run("multi-address cluster and explicit TLS", func(t *testing.T) {
		opt, err := BuildClientOption(Config{
			ValkeyAddr:    "node1.cache.amazonaws.com:6379, node2.cache.amazonaws.com:6379",
			Username:      "labbit-app",
			Password:      "top-secret-valkey-pass",
			TLSEnabled:    true,
			TLSServerName: "clustercfg.labbit.serverless.apn2.cache.amazonaws.com",
		})
		if err != nil {
			t.Fatalf("BuildClientOption err = %v", err)
		}
		if len(opt.InitAddress) != 2 || opt.InitAddress[0] != "node1.cache.amazonaws.com:6379" || opt.InitAddress[1] != "node2.cache.amazonaws.com:6379" {
			t.Fatalf("unexpected InitAddress: %+v", opt.InitAddress)
		}
		if opt.Username != "labbit-app" || opt.Password != "top-secret-valkey-pass" {
			t.Fatalf("unexpected auth credentials in option")
		}
		if opt.TLSConfig == nil || opt.TLSConfig.MinVersion != tls.VersionTLS12 {
			t.Fatalf("expected TLS 1.2+ config, got %+v", opt.TLSConfig)
		}
		if opt.TLSConfig.ServerName != "clustercfg.labbit.serverless.apn2.cache.amazonaws.com" {
			t.Fatalf("ServerName = %q", opt.TLSConfig.ServerName)
		}
	})

	t.Run("valkeys URL scheme enables TLS and infers ServerName", func(t *testing.T) {
		opt, err := BuildClientOption(Config{
			ValkeyAddr: "valkeys://default:my-url-secret@valkey.serverless.apn2.cache.amazonaws.com:6379",
		})
		if err != nil {
			t.Fatalf("BuildClientOption err = %v", err)
		}
		if len(opt.InitAddress) != 1 || opt.InitAddress[0] != "valkey.serverless.apn2.cache.amazonaws.com:6379" {
			t.Fatalf("unexpected InitAddress: %+v", opt.InitAddress)
		}
		if opt.Username != "default" || opt.Password != "my-url-secret" {
			t.Fatalf("unexpected URL credentials")
		}
		if opt.TLSConfig == nil || opt.TLSConfig.ServerName != "valkey.serverless.apn2.cache.amazonaws.com" {
			t.Fatalf("expected TLS ServerName inferred from URL host, got %+v", opt.TLSConfig)
		}
	})

	t.Run("malformed URL never leaks embedded password in error", func(t *testing.T) {
		secret := "ultra-sensitive-password-999"
		_, err := BuildClientOption(Config{
			ValkeyAddr: "valkeys://user:" + secret + "@:bad",
		})
		if err == nil {
			t.Fatal("expected error for malformed URL")
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked secret: %v", err)
		}
	})
}

func TestLoadSecretValue_FileAndEnvPrecedence(t *testing.T) {
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "valkey-password")
	if err := os.WriteFile(secretFile, []byte("  file-secret-value \n"), 0o600); err != nil {
		t.Fatal(err)
	}

	const fileKey = "TEST_LABBIT_VALKEY_PASSWORD_FILE"
	const rawKey = "TEST_LABBIT_VALKEY_PASSWORD"
	t.Setenv(fileKey, secretFile)
	t.Setenv(rawKey, "env-fallback-value")

	val, err := LoadSecretValue("production", fileKey, rawKey, true)
	if err != nil {
		t.Fatalf("LoadSecretValue from file err = %v", err)
	}
	if val != "file-secret-value" {
		t.Fatalf("got %q, want %q", val, "file-secret-value")
	}

	// Production mode must reject raw env secret when file env is unset
	t.Setenv(fileKey, "")
	t.Setenv(rawKey, "raw-prod-secret")
	if _, err := LoadSecretValue("production", fileKey, rawKey, false); err == nil {
		t.Fatal("expected error when raw env secret is used in production")
	}

	// Non-production mode permits raw env secret
	t.Setenv(rawKey, "  dev-env-secret\n")
	val, err = LoadSecretValue("development", fileKey, rawKey, false)
	if err != nil || val != "dev-env-secret" {
		t.Fatalf("got %q, err=%v; want dev-env-secret", val, err)
	}
}

func TestValidateTargetAddr_SSRFAndPortGuards(t *testing.T) {
	self := "10.42.0.10:8080"
	rejected := []string{
		"",
		"10.42.0.11",
		"10.42.0.11:0",
		"10.42.0.11:9090", // admin port
		self,              // self loop
		"10.42.0.11:8080/api/v1",
		"169.254.169.254:80",   // AWS IMDSv1/v2 IPv4
		"169.254.169.254:8080", // Link-local IPv4
		"[fd00:ec2::254]:80",   // AWS IMDS IPv6
		"[fe80::1]:8080",       // Link-local IPv6
		"0.0.0.0:8080",         // Unspecified IPv4
		"[::]:8080",            // Unspecified IPv6
		"224.0.0.1:8080",       // Multicast
		"metadata.google.internal:8080",
		"instance-data:8080",
	}
	for _, target := range rejected {
		if err := ValidateTargetAddr(target, self); err == nil {
			t.Errorf("ValidateTargetAddr(%q) succeeded, expected rejection", target)
		}
	}

	allowed := []string{
		"10.42.0.11:8080",
		"127.0.0.1:8081",
		"backend-1.backend-headless.default.svc.cluster.local:8080",
	}
	for _, target := range allowed {
		if err := ValidateTargetAddr(target, self); err != nil {
			t.Errorf("ValidateTargetAddr(%q) failed unexpectedly: %v", target, err)
		}
	}
}

func TestProxy_PeerAuthSpoofingAndLoopPrevention(t *testing.T) {
	proxy := NewProxyWithSelf("correct-peer-secret", "10.0.0.1:8080")

	t.Run("spoofed header detection and constant-time peer check", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
		if proxy.IsInternalPeer(req) || proxy.HasSpoofedPeerHeader(req) {
			t.Fatal("clean external request should not be marked internal or spoofed")
		}

		// Spoofed X-Labbit-Internal-Peer without valid secret
		reqSpoof := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
		reqSpoof.Header.Set(HeaderInternalPeer, "true")
		reqSpoof.Header.Set(HeaderPeerSecret, "wrong-secret")
		if proxy.IsInternalPeer(reqSpoof) {
			t.Fatal("spoofed secret must not pass IsInternalPeer")
		}
		if !proxy.HasSpoofedPeerHeader(reqSpoof) {
			t.Fatal("spoofed header must be detected by HasSpoofedPeerHeader")
		}

		// Valid internal peer header
		reqValid := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
		reqValid.Header.Set(HeaderInternalPeer, "true")
		reqValid.Header.Set(HeaderPeerSecret, "correct-peer-secret")
		if !proxy.IsInternalPeer(reqValid) || proxy.HasSpoofedPeerHeader(reqValid) {
			t.Fatal("valid internal peer request failed verification")
		}

		// Multi-hop loop prevention: ProxyHTTP rejects request that already carries internal peer header
		rec := httptest.NewRecorder()
		proxy.ProxyHTTP(rec, reqValid, "10.0.0.2:8080")
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 on multi-hop peer loop, got %d", rec.Code)
		}

		// SSRF target rejection: ProxyHTTP rejects metadata IP
		recSSRF := httptest.NewRecorder()
		proxy.ProxyHTTP(recSSRF, req, "169.254.169.254:80")
		if recSSRF.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 on SSRF target, got %d", recSSRF.Code)
		}
	})

	t.Run("websocket proxy preserves close codes and large binary frames", func(t *testing.T) {
		upgrader := websocket.Upgrader{Subprotocols: []string{"labbit.terminal.v1"}}
		var receivedPeerHeader bool
		var receivedLargeLen int
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedPeerHeader = proxy.IsInternalPeer(r)
			ws, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer ws.Close()
			// Read initial frame forwarded by ProxyWebSocketUpstream
			_, _, err = ws.ReadMessage()
			if err != nil {
				return
			}
			// Read 32 KiB binary frame (> 4096 control frame limit)
			_, msg, err := ws.ReadMessage()
			if err != nil {
				return
			}
			receivedLargeLen = len(msg)
			// Close with custom code 4008 (GraceExpired)
			_ = ws.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(4008, "grace expired"),
				time.Now().Add(time.Second),
			)
		}))
		defer upstream.Close()
		upstreamAddr := strings.TrimPrefix(upstream.URL, "http://")

		gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ws, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer ws.Close()
			// Simulate browser.go setting 4096 read limit for initial control frame
			ws.SetReadLimit(4096)
			_, initMsg, err := ws.ReadMessage()
			if err != nil {
				return
			}
			_ = proxy.ProxyWebSocketUpstream(ws, upstreamAddr, "/ws/v1/Test", initMsg, "labbit.terminal.v1", nil)
		}))
		defer gateway.Close()

		wsURL := "ws" + strings.TrimPrefix(gateway.URL, "http") + "/ws/v1/Test"
		dialer := websocket.Dialer{Subprotocols: []string{"labbit.terminal.v1"}}
		clientWS, _, err := dialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("client dial failed: %v", err)
		}
		defer clientWS.Close()

		if err := clientWS.WriteMessage(websocket.TextMessage, []byte(`{"type":"TERMINAL_ATTACH"}`)); err != nil {
			t.Fatalf("write init message: %v", err)
		}
		largePayload := make([]byte, 32*1024)
		if err := clientWS.WriteMessage(websocket.BinaryMessage, largePayload); err != nil {
			t.Fatalf("write large binary message: %v", err)
		}

		_ = clientWS.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, _, err = clientWS.ReadMessage()
		var closeErr *websocket.CloseError
		if !errors.As(err, &closeErr) || closeErr.Code != 4008 {
			t.Fatalf("expected CloseError code 4008, got err=%v", err)
		}
		if !receivedPeerHeader {
			t.Fatal("upstream did not receive valid internal peer header")
		}
		if receivedLargeLen != len(largePayload) {
			t.Fatalf("upstream received %d bytes, want %d", receivedLargeLen, len(largePayload))
		}
	})
}

func TestLocator_LookupRetiresLocalStaleLeaseImmediately(t *testing.T) {
	locA, err := NewLocator(Config{
		ValkeyAddr: "127.0.0.1:6379",
		PodID:      "pod-a",
		PodAddr:    "127.0.0.1:8081",
	})
	if err != nil {
		t.Skipf("Valkey unavailable: %v", err)
	}
	defer locA.Close()

	locB, err := NewLocator(Config{
		ValkeyAddr: "127.0.0.1:6379",
		PodID:      "pod-b",
		PodAddr:    "127.0.0.1:8082",
	})
	if err != nil {
		t.Skipf("Valkey unavailable: %v", err)
	}
	defer locB.Close()

	ctx := context.Background()
	connectorID := uuid.New()
	lostA := make(chan struct{})
	// Use a long TTL (30s -> 10s heartbeat) so background tick won't fire during this check
	epochA, err := locA.RegisterConnector(ctx, connectorID, 0, 30*time.Second, func() {
		close(lostA)
	})
	if err != nil {
		t.Fatalf("RegisterConnector A: %v", err)
	}

	epochB, err := locB.RegisterConnector(ctx, connectorID, 0, 30*time.Second)
	if err != nil {
		t.Fatalf("RegisterConnector B: %v", err)
	}
	if epochB <= epochA {
		t.Fatalf("expected epochB (%d) > epochA (%d)", epochB, epochA)
	}
	defer locB.ReleaseConnector(ctx, connectorID, epochB)

	// Calling LookupConnector on Pod A must immediately detect Pod B's takeover and retire Pod A's local lease
	owner, err := locA.LookupConnector(ctx, connectorID)
	if err != nil {
		t.Fatalf("LookupConnector on Pod A: %v", err)
	}
	if owner.PodID != "pod-b" || owner.Epoch != epochB {
		t.Fatalf("unexpected owner: %+v", owner)
	}
	select {
	case <-lostA:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected LookupConnector on Pod A to immediately invoke onLost callback upon observing newer owner")
	}
}
