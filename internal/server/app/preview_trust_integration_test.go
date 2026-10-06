//go:build integration

package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connectorwss"
	"github.com/ktcloud4-SL/labbit-app/internal/server/preview"
	"github.com/ktcloud4-SL/labbit-app/internal/server/previewsession"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
)

// ---- Browser 인증과 만료 ----

func TestPreviewBootstrapCredentialExpiresAndIsSingleUse(t *testing.T) {
	e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1, fakeClock: true})
	s := e.mustCreate(e.ownerCookie, e.fixture.LabInstanceID, 5173)

	// bootstrap credential은 짧은 시간 안에만 교환할 수 있다. 지나면 PreviewSession이 살아 있어도 교환할 수 없다.
	e.clock.Advance(3 * time.Minute)
	resp, cookie := e.exchange(s)
	if resp.StatusCode != http.StatusUnauthorized || cookie != nil {
		t.Fatalf("만료된 credential exchange = %d %v", resp.StatusCode, cookie)
	}
	if r, _ := e.get(s, nil, "/"); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Cookie 없는 요청 status = %d", r.StatusCode)
	}

	// 새 PreviewSession의 credential은 한 번만 성공한다.
	s2 := e.mustCreate(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	if resp, cookie := e.exchange(s2); resp.StatusCode != http.StatusNoContent || cookie == nil {
		t.Fatalf("exchange = %d", resp.StatusCode)
	}
	if resp, cookie := e.exchange(s2); resp.StatusCode != http.StatusUnauthorized || cookie != nil {
		t.Fatalf("재사용 exchange = %d", resp.StatusCode)
	}
	// 다른 PreviewSession의 credential도 쓸 수 없다.
	s3 := e.mustCreate(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	crossed := s3
	crossed.Credential = s.Credential
	if resp, _ := e.exchange(crossed); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("다른 PreviewSession의 credential status = %d", resp.StatusCode)
	}
}

// 만료 뒤에는 Cookie 인증이 무효이고 tunnel이 닫히며 Connector에 PREVIEW_CLOSE가 간다.
func TestPreviewSessionExpiryInvalidatesAuthAndClosesTheTunnel(t *testing.T) {
	e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1, fakeClock: true})
	s := e.mustCreate(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	cookie := e.login(s)
	if !s.ExpiresAt.Equal(e.clock.Now().Add(time.Hour)) {
		t.Fatalf("expiresAt = %v, want 생성 완료 시점 + TTL(1h)", s.ExpiresAt)
	}

	e.clock.Advance(59 * time.Minute)
	if r, _ := e.get(s, cookie, "/"); r.StatusCode != http.StatusOK {
		t.Fatalf("만료 전 status = %d", r.StatusCode)
	}
	e.clock.Advance(2 * time.Minute)
	if r, body := e.get(s, cookie, "/"); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("만료 뒤 status = %d %s, want 401", r.StatusCode, body)
	}
	eventually(t, "PREVIEW_CLOSE", 5*time.Second, func() bool { return len(e.peer.Closes()) == 1 })
	if c := e.peer.Closes()[0]; c.Reason != "SESSION_EXPIRED" || c.PreviewSessionID != s.ID {
		t.Fatalf("PREVIEW_CLOSE = %+v", c)
	}
	if code, ok := e.peer.DataCloseCode(s.ID, 5*time.Second); !ok || code != websocket.CloseNormalClosure {
		t.Fatalf("Data WSS close = %d, %v", code, ok)
	}
	// 이미 만료된 PreviewSession을 닫아도 성공이다(멱등).
	if resp := e.request(http.MethodDelete, "/api/v1/preview-sessions/"+s.ID, e.ownerCookie, "", nil); resp.Status != http.StatusNoContent {
		t.Fatalf("만료된 PreviewSession DELETE status = %d: %s", resp.Status, resp.Body)
	}
}

func TestPreviewSessionExplicitCloseIsImmediateAndOwnerOnly(t *testing.T) {
	e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})
	s := e.mustCreate(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	cookie := e.login(s)
	path := "/api/v1/preview-sessions/" + s.ID

	// 다른 사용자(같은 Class의 학생, 강사, 다른 Class, 다른 Organization)는 닫을 수 없다.
	for name, c := range map[string]string{"학생": e.peerCookie, "강사": e.instructorCookie, "다른 Class": e.outsiderCookie, "다른 Organization": e.foreignCookie} {
		if resp := e.request(http.MethodDelete, path, c, "", nil); resp.Status != http.StatusForbidden {
			t.Errorf("%s DELETE status = %d, want 403", name, resp.Status)
		}
	}
	if resp := e.request(http.MethodDelete, path, "", "", nil); resp.Status != http.StatusUnauthorized {
		t.Errorf("로그인 없는 DELETE status = %d", resp.Status)
	}
	if r, _ := e.get(s, cookie, "/"); r.StatusCode != http.StatusOK {
		t.Fatalf("거절된 DELETE가 PreviewSession에 영향을 줌: %d", r.StatusCode)
	}
	if resp := e.request(http.MethodDelete, "/api/v1/preview-sessions/"+uuid.NewString(), e.ownerCookie, "", nil); resp.Status != http.StatusNotFound {
		t.Errorf("알 수 없는 ID DELETE status = %d, want 404", resp.Status)
	}
	if resp := e.request(http.MethodDelete, "/api/v1/preview-sessions/not-a-uuid", e.ownerCookie, "", nil); resp.Status != http.StatusNotFound {
		t.Errorf("잘못된 ID DELETE status = %d, want 404", resp.Status)
	}

	// 소유자의 종료는 즉시 인증을 무효로 하고 tunnel을 닫고 Connector에 알린다.
	if resp := e.request(http.MethodDelete, path, e.ownerCookie, "", nil); resp.Status != http.StatusNoContent {
		t.Fatalf("DELETE status = %d: %s", resp.Status, resp.Body)
	}
	if r, _ := e.get(s, cookie, "/"); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("종료 뒤 Cookie 요청 status = %d, want 401", r.StatusCode)
	}
	if resp, _ := e.exchange(s); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("종료 뒤 exchange status = %d", resp.StatusCode)
	}
	eventually(t, "PREVIEW_CLOSE", 5*time.Second, func() bool { return len(e.peer.Closes()) == 1 })
	if c := e.peer.Closes()[0]; c.Reason != "SESSION_CLOSED" || c.PreviewSessionID != s.ID {
		t.Fatalf("PREVIEW_CLOSE = %+v", c)
	}
	if code, ok := e.peer.DataCloseCode(s.ID, 5*time.Second); !ok || code != websocket.CloseNormalClosure {
		t.Fatalf("Data WSS close = %d, %v", code, ok)
	}
	// 반복 DELETE는 204이고 PREVIEW_CLOSE를 다시 보내지 않는다.
	if resp := e.request(http.MethodDelete, path, e.ownerCookie, "", nil); resp.Status != http.StatusNoContent {
		t.Fatalf("반복 DELETE status = %d", resp.Status)
	}
	time.Sleep(100 * time.Millisecond)
	if got := len(e.peer.Closes()); got != 1 {
		t.Fatalf("반복 DELETE 뒤 PREVIEW_CLOSE %d개", got)
	}
}

// Reset/Cleanup이 호출할 경계다. LabInstance의 모든 PreviewSession이 종료된다(Reset/Cleanup 자체는 이 PR의 범위가 아니다).
func TestCloseForLabMutationEndsTheLabsPreviewSessions(t *testing.T) {
	e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})
	a := e.mustCreate(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	b := e.mustCreate(e.ownerCookie, e.fixture.LabInstanceID, 3000)
	peerSession := e.mustCreate(e.peerCookie, e.fixture.PeerLabInstanceID, 5173)
	ca, cb, cp := e.login(a), e.login(b), e.login(peerSession)

	n, err := e.stack.Previews.CloseForLabMutation(context.Background(), previewsession.LabMutation{LabInstanceID: e.fixture.LabInstanceID, Reason: preview.EndLabReset})
	if err != nil || n != 2 {
		t.Fatalf("CloseForLabMutation() = %d, %v", n, err)
	}
	for _, x := range []struct {
		s session
		c *http.Cookie
	}{{a, ca}, {b, cb}} {
		if r, _ := e.get(x.s, x.c, "/"); r.StatusCode != http.StatusUnauthorized {
			t.Errorf("Reset한 LabInstance의 PreviewSession status = %d, want 401", r.StatusCode)
		}
	}
	// 다른 LabInstance(다른 사용자)의 PreviewSession은 영향이 없다.
	if r, _ := e.get(peerSession, cp, "/"); r.StatusCode != http.StatusOK {
		t.Fatalf("다른 LabInstance의 PreviewSession status = %d", r.StatusCode)
	}
	eventually(t, "PREVIEW_CLOSE", 5*time.Second, func() bool { return len(e.peer.Closes()) == 2 })
	for _, c := range e.peer.Closes() {
		if c.Reason != "LAB_RESET" {
			t.Fatalf("PREVIEW_CLOSE = %+v", c)
		}
	}
	if _, err := e.stack.Previews.CloseForLabMutation(context.Background(), previewsession.LabMutation{LabInstanceID: e.fixture.LabInstanceID, Reason: "OTHER"}); err == nil {
		t.Fatal("잘못된 reason을 받아들임")
	}
}

// ---- trust: Connector 연결과 Credential ----

// Connector와 Workspace VM 사이의 연결이 끊기면(tunnel 손실) PreviewSession이 끝난다. Connector가 이미 아는 일이라 PREVIEW_CLOSE를 보내지 않는다.
func TestPreviewTunnelLossEndsTheSession(t *testing.T) {
	e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})
	s := e.mustCreate(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	cookie := e.login(s)

	e.peer.DropData(s.ID)
	eventually(t, "PreviewSession 종료", 5*time.Second, func() bool {
		r, _ := e.get(s, cookie, "/")
		return r.StatusCode == http.StatusUnauthorized
	})
	time.Sleep(100 * time.Millisecond)
	if closes := e.peer.Closes(); len(closes) != 0 {
		t.Fatalf("tunnel 손실에서 PREVIEW_CLOSE를 보냄: %+v", closes)
	}
	// 새 PreviewSession을 만들 수 있다.
	e.mustCreate(e.ownerCookie, e.fixture.LabInstanceID, 5173)
}

// Credential이 revoke되면 그 Credential로 인증된 Preview Data WSS를 더 이상 신뢰하지 않고 close 4001로 끝낸다. 같은 Credential의 새 Upgrade는 401이다.
func TestPreviewCredentialRevokeClosesTheTunnelAndRejectsNewUpgrades(t *testing.T) {
	e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})
	s := e.mustCreate(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	cookie := e.login(s)

	var credentialID uuid.UUID
	if err := e.conn.QueryRow(t.Context(), `SELECT id FROM connector_credentials WHERE connector_id = $1`, e.fixture.ConnectorID).Scan(&credentialID); err != nil {
		t.Fatal(err)
	}
	// revoke use case가 저장소 상태를 바꾼 뒤 lifecycle hook으로 Registry에 알린다.
	terminaltest.Exec(t, e.conn, `UPDATE connector_credentials SET revoked_at = now() WHERE id = $1`, credentialID)
	e.stack.Registry.RevokeCredential(credentialID)

	if code, ok := e.peer.DataCloseCode(s.ID, 5*time.Second); !ok || code != 4001 {
		t.Fatalf("Data WSS close = %d, %v, want 4001", code, ok)
	}
	if r, _ := e.get(s, cookie, "/"); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoke 뒤 요청 status = %d, want 401", r.StatusCode)
	}
	dialer := websocket.Dialer{Subprotocols: []string{"labbit.connector-preview.v1"}, HandshakeTimeout: 5 * time.Second}
	if _, resp, err := dialer.Dial(e.wsBase()+preview.DataPath, http.Header{"Authorization": {"Bearer " + terminaltest.Credential}}); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoke된 Credential의 새 Data WSS = %v %v, want 401", err, resp)
	}
}

// 같은 Connector의 새 Control connection이 이전 Control Session을 교체하면 이전 Session으로 시작한 PreviewSession의 tunnel은 trust를 잃는다.
// 새 Control Session은 자신의 PreviewSession을 새로 만든다.
func TestPreviewControlSessionReplacementEndsTheOldTunnel(t *testing.T) {
	e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})
	old := e.mustCreate(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	oldCookie := e.login(old)

	// 같은 Credential의 새 Control connection이 이전 connection을 교체한다.
	second := e.startPeer(previewV1)
	eventually(t, "새 Session protocol-ready", 10*time.Second, func() bool {
		return e.stack.Registry.WithReadyRoute(e.fixture.ConnectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})

	if code, ok := e.peer.DataCloseCode(old.ID, 5*time.Second); !ok || code != 4002 {
		t.Fatalf("교체된 Control Session의 tunnel close = %d, %v, want 4002", code, ok)
	}
	if r, _ := e.get(old, oldCookie, "/"); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("교체 뒤 이전 PreviewSession 요청 status = %d, want 401", r.StatusCode)
	}

	// 새 Control Session으로 새 PreviewSession을 만든다. PREVIEW_OPEN은 새 peer가 받는다.
	fresh := e.mustCreate(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	if len(second.Opens()) != 1 || second.Opens()[0].PreviewSessionID != fresh.ID {
		t.Fatalf("새 peer의 PREVIEW_OPEN = %+v", second.Opens())
	}
	freshCookie := e.login(fresh)
	if r, body := e.get(fresh, freshCookie, "/"); r.StatusCode != http.StatusOK || !strings.Contains(body, "workspace says hello") {
		t.Fatalf("새 PreviewSession 요청 = %d %q", r.StatusCode, body)
	}
}

// ---- 종료(shutdown) ----

func TestPreviewShutdownClosesSessionsAndNotifiesTheConnector(t *testing.T) {
	e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})
	s := e.mustCreate(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	cookie := e.login(s)

	e.stack.close()

	// 활성 PreviewSession은 SERVICE_RESTARTING으로 끝나고 Connector는 PREVIEW_CLOSE와 Data WSS close 1001을 받는다.
	eventually(t, "PREVIEW_CLOSE", 5*time.Second, func() bool { return len(e.peer.Closes()) == 1 })
	if c := e.peer.Closes()[0]; c.Reason != "SERVICE_RESTARTING" || c.PreviewSessionID != s.ID {
		t.Fatalf("PREVIEW_CLOSE = %+v", c)
	}
	if code, ok := e.peer.DataCloseCode(s.ID, 5*time.Second); !ok || code != websocket.CloseGoingAway {
		t.Fatalf("Data WSS close = %d, %v, want 1001", code, ok)
	}
	// 종료 중에는 새 Preview 요청, 새 PreviewSession, 새 Data WSS를 받지 않는다.
	if r, body := e.get(s, cookie, "/"); r.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "preview_unavailable") {
		t.Fatalf("종료 중 요청 = %d %s", r.StatusCode, body)
	}
	resp := e.create(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	if resp.Status != http.StatusServiceUnavailable || e.problemCode(resp) != "preview_unavailable" {
		t.Fatalf("종료 중 create = %d %s", resp.Status, resp.Body)
	}
	dialer := websocket.Dialer{Subprotocols: []string{"labbit.connector-preview.v1"}, HandshakeTimeout: 5 * time.Second}
	if _, resp, err := dialer.Dial(e.wsBase()+preview.DataPath, http.Header{"Authorization": {"Bearer " + terminaltest.Credential}}); err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("종료 중 새 Data WSS = %v %v, want 503", err, resp)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.stack.Previews.Shutdown(ctx); err != nil {
		t.Fatalf("Previews.Shutdown() error = %v", err)
	}
}

// ---- Origin 격리 ----

// Preview Origin(사용자 코드)에서는 SaaS 본 서비스 API에 도달할 수 없다. 같은 경로도 Workspace application의 것이며 로그인 Session Cookie는 전달되지 않는다.
func TestPreviewOriginIsIsolatedFromTheMainService(t *testing.T) {
	e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})
	s := e.mustCreate(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	cookie := e.login(s)
	e.app.setHandler(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "application %s", r.URL.Path)
	})

	// 로그인 Session Cookie와 함께 SaaS API 경로를 Preview Origin으로 요청해도 SaaS API가 응답하지 않고 Workspace application으로 간다.
	resp, body := e.get(s, cookie, "/api/v1/me", func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: realtime.SessionCookieName, Value: e.ownerCookie})
		r.Header.Set("Origin", fileTrustedOrigin)
	})
	if resp.StatusCode != http.StatusOK || body != "application /api/v1/me" {
		t.Fatalf("Preview Origin의 /api/v1/me = %d %q, want Workspace application의 응답", resp.StatusCode, body)
	}
	for _, r := range e.app.all() {
		if strings.Contains(fmt.Sprint(r.Header), e.ownerCookie) {
			t.Fatal("로그인 Session Cookie가 Workspace application에 전달됨")
		}
	}
	// 인증 없이 SaaS API 경로를 Preview Origin으로 요청하면 Gateway의 401이다(SaaS API의 401이 아니다).
	if r, b := e.get(s, nil, "/api/v1/me"); r.StatusCode != http.StatusUnauthorized || !strings.Contains(b, "preview_unauthenticated") {
		t.Fatalf("Cookie 없는 /api/v1/me = %d %s", r.StatusCode, b)
	}
	// 반대로 본 서비스 host로는 Preview Gateway의 예약 경로에 도달하지 않는다.
	if resp := e.request(http.MethodGet, preview.BootstrapPath, "", "", nil); resp.Status != http.StatusNotFound {
		t.Fatalf("본 서비스 host의 /__labbit/bootstrap status = %d, want 404", resp.Status)
	}
	// 로그인한 본 서비스 API는 그대로 동작한다.
	if resp := e.request(http.MethodGet, "/api/v1/me", e.ownerCookie, "", nil); resp.Status != http.StatusOK {
		t.Fatalf("본 서비스 /api/v1/me status = %d", resp.Status)
	}
}

// ---- 실제 Run 조립 ----

// api와 preview role을 함께 enabled하면 Preview가 조립되고 readiness가 ready가 된다. 설정은 LoadConfig로 읽는다.
func TestServerWithAPIAndPreviewRolesAssemblesPreview(t *testing.T) {
	dsn := postgrestest.NewDatabase(t)
	postgrestest.Migrate(t, dsn, loadEmbeddedMigrations(t))
	t.Setenv("LABBIT_PREVIEW_ALLOWED_PORTS", "3000,5173")
	t.Setenv("LABBIT_PREVIEW_SESSION_TTL", "30m")
	t.Setenv("LABBIT_PREVIEW_ORIGIN_TEMPLATE", "https://{sessionId}.preview.labbit.test")

	admin, application := startServerWithApplication(t, "development", "api,preview", dsn, "")
	waitForStatus(t, admin+"/readyz", http.StatusOK)

	// Connector Preview Data endpoint가 mount되어 있다(WebSocket Upgrade가 아니라서 400이다).
	assertStatus(t, application+preview.DataPath, http.StatusBadRequest)
	// PreviewSession 생성은 인증을 요구한다.
	resp, err := http.Post(application+"/api/v1/lab-instances/"+uuid.NewString()+"/preview-sessions", "application/json", strings.NewReader(`{"targetPort":3000}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("인증 없는 create status = %d", resp.StatusCode)
	}
	// Preview Origin host의 요청은 Gateway가 처리한다(알 수 없는 PreviewSession이라 401).
	req, _ := http.NewRequest(http.MethodGet, application+"/", nil)
	req.Host = uuid.NewString() + ".preview.labbit.test"
	presp, err := probeClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(presp.Body)
	presp.Body.Close()
	if presp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), "preview_unauthenticated") {
		t.Fatalf("Preview Origin host = %d %s, want Gateway의 401", presp.StatusCode, body)
	}
	// 본 서비스 host의 같은 경로는 Gateway가 아니다.
	assertStatus(t, application+"/", http.StatusNotFound)
}

// preview role만 enabled된 process는 PostgreSQL DSN 없이 시작하지만 authority가 없어 Preview route를 열지 않고 not-ready다.
func TestServerWithOnlyThePreviewRoleIsNotReadyAndServesNothing(t *testing.T) {
	t.Setenv("LABBIT_PREVIEW_ALLOWED_PORTS", "3000")
	t.Setenv("LABBIT_PREVIEW_SESSION_TTL", "30m")
	t.Setenv("LABBIT_PREVIEW_ORIGIN_TEMPLATE", "https://{sessionId}.preview.labbit.test")

	admin, application := startServerWithApplication(t, "development", "preview", "", "")
	waitForStatus(t, admin+"/readyz", http.StatusServiceUnavailable)
	assertStatus(t, admin+"/livez", http.StatusOK)

	// 인증 없는 Preview route를 열지 않는다.
	assertStatus(t, application+preview.DataPath, http.StatusNotFound)
	req, _ := http.NewRequest(http.MethodGet, application+"/", nil)
	req.Host = uuid.NewString() + ".preview.labbit.test"
	resp, err := probeClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("Preview Origin host status = %d, want 404", resp.StatusCode)
	}
	assertStatus(t, application+connectorwss.Path, http.StatusNotFound)
}
