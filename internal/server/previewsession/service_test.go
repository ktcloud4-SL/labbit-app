package previewsession

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/preview"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// ---- 권한 (Create는 Connector를 호출하기 전에 모든 조건을 확인한다) ----

// 거절된 요청은 Connector Control이나 Gateway에 어떤 side effect도 만들지 않는다.
func (f *fixture) assertNoSideEffects() {
	f.t.Helper()
	if f.connectors.calls() != 0 {
		f.t.Fatalf("거절된 요청이 Connector에 message %d개를 보냄", f.connectors.calls())
	}
	if f.gateway.expectedCount() != 0 {
		f.t.Fatal("거절된 요청이 Gateway에 PreviewSession을 등록함")
	}
	if len(f.gateway.forgotIDs()) != 0 {
		f.t.Fatal("거절된 요청이 정리할 것이 없는데 Forget함")
	}
}

func TestCreateOwnReadyLabInstanceResolvesTheTargetAndWaitsForTheTunnel(t *testing.T) {
	f := newFixture(t)
	created, err := f.create(5173)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := uuid.Parse(created.ID); err != nil || created.TargetPort != 5173 ||
		created.PreviewURL != f.gateway.activation.URL || !created.ExpiresAt.Equal(f.gateway.activation.ExpiresAt) {
		t.Fatalf("Created = %+v", created)
	}

	// Gateway에는 서버가 결정한 값만 등록한다. Browser가 보낸 것은 port뿐이다.
	x := f.gateway.lastExpected(t)
	if x.SessionID != created.ID || x.OwnerID != ownerID.String() || x.OrganizationID != orgID.String() ||
		x.ConnectorID != connectorID || x.LabInstanceID != labID.String() || x.Generation != 3 ||
		x.TargetVMKey != "vk-web" || x.ProviderServerID != "srv-web-g3" || x.TargetPort != 5173 ||
		x.TTL != time.Hour || x.RequestID != "request-1" {
		t.Fatalf("Expected = %+v", x)
	}

	// Connector에는 승인한 정확한 port와 resolved target만 보낸다.
	if f.connectors.openCount() != 1 {
		t.Fatalf("PREVIEW_OPEN %d개, want 1개", f.connectors.openCount())
	}
	open := f.connectors.opens[0]
	if open.ConnectorID != connectorID || open.RequestID != "request-1" || open.TargetVMKey != "vk-web" || open.ProviderServerID != "srv-web-g3" ||
		open.TargetPort != 5173 || open.Correlation != (connector.PreviewCorrelation{PreviewSessionID: created.ID, LabInstanceID: labID.String(), Generation: 3}) {
		t.Fatalf("PreviewOpen = %+v", open)
	}
	// PREVIEW_OPEN을 받는 Control Session에 PreviewSession을 묶었다.
	if got := f.gateway.bound[created.ID]; got != (preview.Binding{ConnectorID: connectorID, ControlSessionID: controlID, CredentialID: credID}) {
		t.Fatalf("binding = %+v", got)
	}
	if f.connectors.wasInTransaction {
		t.Fatal("PostgreSQL transaction이 열린 채 Connector에 PREVIEW_OPEN을 보냄")
	}
	if got := f.gateway.activatedIDs(); len(got) != 1 || got[0] != created.ID {
		t.Fatalf("Activate = %v", got)
	}
	if len(f.connectors.closeList()) != 0 || len(f.gateway.forgotIDs()) != 0 {
		t.Fatal("성공한 생성이 정리를 수행함")
	}
}

func TestCreateRejectsOtherUsersOrganizationsAndMissingMembershipWithoutSideEffects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*fakeStore)
		user   repository.User
		want   error
	}{
		{"다른 사용자의 LabInstance", func(*fakeStore) {}, repository.User{ID: otherID, OrganizationID: orgID, OrganizationRole: repository.OrganizationRoleMember}, ErrForbidden},
		{"Organization ADMIN도 다른 사용자의 것은 안 됨", func(*fakeStore) {}, repository.User{ID: otherID, OrganizationID: orgID, OrganizationRole: repository.OrganizationRoleAdmin}, ErrForbidden},
		{"다른 Organization", func(*fakeStore) {}, repository.User{ID: ownerID, OrganizationID: otherOrgID, OrganizationRole: repository.OrganizationRoleMember}, ErrForbidden},
		{"LabInstance가 다른 Organization의 것", func(s *fakeStore) { s.lab.OrganizationID = otherOrgID }, owner, ErrForbidden},
		{"ClassMembership 없음", func(s *fakeStore) { s.memberErr = repository.ErrNotFound }, owner, ErrForbidden},
		{"Membership이 다른 Class", func(s *fakeStore) { s.membership.ClassID = uuid.New() }, owner, ErrInconsistentData},
		{"Membership이 다른 사용자", func(s *fakeStore) { s.membership.UserID = otherID }, owner, ErrInconsistentData},
		{"Membership이 다른 Organization", func(s *fakeStore) { s.membership.OrganizationID = otherOrgID }, owner, ErrInconsistentData},
		{"Membership role이 올바르지 않음", func(s *fakeStore) { s.membership.Role = "" }, owner, ErrInconsistentData},
		{"LabInstance 없음", func(s *fakeStore) { s.labErr = repository.ErrNotFound }, owner, ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.mutate(f.store)
			_, err := f.service.Create(context.Background(), tc.user, CreateInput{LabInstanceID: labID.String(), TargetPort: 3000})
			errIs(t, err, tc.want)
			f.assertNoSideEffects()
		})
	}
}

func TestCreateRejectsAMalformedLabInstanceIDWithoutTouchingTheStore(t *testing.T) {
	for _, id := range []string{"", "not-a-uuid", strings.ToUpper(labID.String()), labID.String() + "x", "{" + labID.String() + "}", strings.ReplaceAll(labID.String(), "-", "")} {
		f := newFixture(t)
		_, err := f.service.Create(context.Background(), owner, CreateInput{LabInstanceID: id, TargetPort: 3000})
		errIs(t, err, ErrNotFound)
		f.assertNoSideEffects()
		f.store.mu.Lock()
		touched := f.store.transactions
		f.store.mu.Unlock()
		if touched != 0 {
			t.Errorf("%q: 형식이 틀린 ID로 저장소를 조회함", id)
		}
	}
}

func TestCreateRejectsALabInstanceThatIsNotReady(t *testing.T) {
	for _, status := range []string{"PENDING", "PROVISIONING", "RESETTING", "FAILED", "DELETING", "DELETED", "", "ready"} {
		f := newFixture(t)
		f.store.lab.Status = status
		_, err := f.create(3000)
		errIs(t, err, ErrLabInstanceNotReady)
		f.assertNoSideEffects()
	}
}

// 저장된 CreationSnapshot과 ProviderResource 관계가 모순이면 값을 만들어 채우지 않고 fail closed한다.
func TestCreateFailsClosedOnInconsistentWorkspaceTargetData(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*fakeStore)
		want   error
	}{
		{"workspaceVmKey가 비어 있음", func(s *fakeStore) { s.snapshot.WorkspaceVMKey = "" }, ErrInconsistentData},
		{"workspaceVmKey가 vms에 없음", func(s *fakeStore) { s.snapshot.WorkspaceVMKey = "vk-ghost" }, ErrInconsistentData},
		{"workspaceVmKey가 vms에 중복", func(s *fakeStore) {
			s.snapshot.VMs = []repository.SnapshotVM{{VMKey: "vk-web"}, {VMKey: "vk-web"}}
		}, ErrInconsistentData},
		{"vms가 비어 있음", func(s *fakeStore) { s.snapshot.VMs = nil }, ErrInconsistentData},
		{"CreationSnapshot 없음", func(s *fakeStore) { s.snapshotErr = repository.ErrNotFound }, ErrInconsistentData},
		{"Connector 관계 없음", func(s *fakeStore) { s.connectorErr = repository.ErrNotFound }, ErrInconsistentData},
		{"generation이 1 미만", func(s *fakeStore) { s.lab.Generation = 0 }, ErrInconsistentData},
		{"PRESENT Workspace VM이 둘", func(s *fakeStore) {
			s.servers["vk-web"] = []repository.ProviderServer{
				{ID: uuid.New(), ProviderID: "srv-a", LifecycleStatus: "PRESENT"},
				{ID: uuid.New(), ProviderID: "srv-b", LifecycleStatus: "PRESENT"},
			}
		}, ErrInconsistentData},
		{"Provider ID가 비어 있음", func(s *fakeStore) {
			s.servers["vk-web"] = []repository.ProviderServer{{ID: uuid.New(), ProviderID: "", LifecycleStatus: "PRESENT"}}
		}, ErrInconsistentData},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.mutate(f.store)
			_, err := f.create(3000)
			errIs(t, err, tc.want)
			f.assertNoSideEffects()
		})
	}
}

// 현재 generation에 PRESENT SERVER ProviderResource가 없으면(Reset 진행, drift) 대상을 사용할 수 없다.
func TestCreateRequiresAPresentServerForTheCurrentGeneration(t *testing.T) {
	cases := map[string][]repository.ProviderServer{
		"ProviderResource 없음": nil,
		"PRESENT가 아님":         {{ID: uuid.New(), ProviderID: "srv-web-g3", LifecycleStatus: "DELETING"}},
		"모두 PRESENT가 아님":      {{ID: uuid.New(), ProviderID: "srv-a", LifecycleStatus: "DELETED"}, {ID: uuid.New(), ProviderID: "srv-b", LifecycleStatus: "UNKNOWN"}},
	}
	for name, servers := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.store.servers["vk-web"] = servers
			_, err := f.create(3000)
			errIs(t, err, ErrTargetUnavailable)
			f.assertNoSideEffects()
		})
	}
}

// 대상 VM은 CreationSnapshot의 workspaceVmKey로 정한다. 다른 VM(vk-db)의 Server를 쓰지 않고 현재 generation으로 조회한다.
func TestCreateResolvesTheWorkspaceVMFromTheSnapshotAndTheCurrentGeneration(t *testing.T) {
	f := newFixture(t)
	f.store.lab.Generation = 7
	f.store.servers["vk-web"] = []repository.ProviderServer{{ID: uuid.New(), ProviderID: "srv-web-g7", LifecycleStatus: "PRESENT"}}
	if _, err := f.create(3000); err != nil {
		t.Fatal(err)
	}
	f.store.mu.Lock()
	queries := append([]serverQuery(nil), f.store.serverQueries...)
	f.store.mu.Unlock()
	if len(queries) != 1 || queries[0] != (serverQuery{generation: 7, name: "vk-web"}) {
		t.Fatalf("ProviderServers 조회 = %+v, want 현재 generation(7)의 workspaceVmKey만", queries)
	}
	x := f.gateway.lastExpected(t)
	if x.ProviderServerID != "srv-web-g7" || x.Generation != 7 || x.TargetVMKey != "vk-web" {
		t.Fatalf("Expected = %+v", x)
	}
}

// ---- 허용 port (권한과 대상 판정 뒤에, Backend 허용 목록에 대해 승인한다) ----

func TestCreateApprovesOnlyPortsInTheBackendAllowList(t *testing.T) {
	f := newFixture(t) // 허용 목록: 3000, 5173, 80
	for _, port := range []int{3000, 5173, 80} {
		if _, err := f.create(port); err != nil {
			t.Errorf("목록에 있는 %d를 승인하지 않음: %v", port, err)
		}
	}
	if f.connectors.openCount() != 3 {
		t.Fatalf("PREVIEW_OPEN %d개", f.connectors.openCount())
	}

	for _, port := range []int{22, 21, 443, 5000, 8080, 3001, 2999, 65535} {
		g := newFixture(t)
		_, err := g.create(port)
		errIs(t, err, ErrPortNotAllowed)
		g.assertNoSideEffects()
	}
}

func TestCreateRejectsPortsThatAreNotIntegersInRange(t *testing.T) {
	for _, port := range []int{0, -1, 65536, 100000} {
		f := newFixture(t)
		_, err := f.create(port)
		errIs(t, err, ErrInvalidPort)
		f.assertNoSideEffects()
	}
}

func TestCreateRejectsTheManagementPortEvenWhenItIsListed(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.Policy = mustPolicy(t, "22,3000") })
	_, err := f.create(22)
	errIs(t, err, ErrPortNotAllowed)
	f.assertNoSideEffects()
	if _, err := f.create(3000); err != nil {
		t.Fatalf("Create(3000) error = %v", err)
	}
}

// 허용 목록이 설정되지 않았으면 어떤 port도 승인하지 않는다.
func TestCreateWithoutAnAllowListApprovesNothing(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.Policy = Policy{} })
	for _, port := range []int{80, 3000, 5000, 5173, 8080} {
		_, err := f.create(port)
		errIs(t, err, ErrPortNotAllowed)
	}
	f.assertNoSideEffects()
}

// 권한 판정이 port 판정보다 먼저다. 다른 사용자는 허용 목록이 무엇인지 알 수 없다.
func TestAuthorizationIsDecidedBeforeThePort(t *testing.T) {
	f := newFixture(t)
	_, err := f.service.Create(context.Background(),
		repository.User{ID: otherID, OrganizationID: orgID, OrganizationRole: repository.OrganizationRoleMember},
		CreateInput{LabInstanceID: labID.String(), TargetPort: 22})
	errIs(t, err, ErrForbidden)
	f.assertNoSideEffects()
}

// ---- Connector 결과 ----

func TestCreateMapsConnectorOpenFailuresAndDoesNotCloseWhatTheConnectorAlreadyFailed(t *testing.T) {
	cases := []struct {
		code string
		want error
	}{
		{"APP_NOT_RUNNING", ErrAppNotRunning},
		{"PORT_REJECTED", ErrPortRejected},
		{"VM_UNREACHABLE", ErrTargetUnreachable},
		{"UNAVAILABLE", ErrOpenFailed},
		{"INTERNAL_ERROR", ErrOpenFailed},
		{"SOMETHING_NEW", ErrOpenFailed},
		{"app_not_running", ErrOpenFailed}, // code는 대소문자를 구분한다.
		{"", ErrOpenFailed},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			f := newFixture(t)
			f.react(f.fail(tc.code))
			_, err := f.create(3000)
			errIs(t, err, tc.want)

			// PreviewSession을 성공 상태로 남기지 않는다. 등록과 pending을 지운다.
			id := f.gateway.lastExpected(t).SessionID
			if got := f.gateway.forgotIDs(); len(got) != 1 || got[0] != id {
				t.Fatalf("Forget = %v, want [%s]", got, id)
			}
			f.connectors.mu.Lock()
			forgotten := append([]string(nil), f.connectors.forgotten...)
			f.connectors.mu.Unlock()
			if len(forgotten) != 1 || forgotten[0] != id {
				t.Fatalf("ForgetPreviewOpen = %v", forgotten)
			}
			if len(f.gateway.activatedIDs()) != 0 {
				t.Fatal("실패한 생성이 Activate됨")
			}
			// Connector가 이미 실패를 알렸으므로 PREVIEW_CLOSE를 보내지 않는다.
			if got := f.connectors.closeList(); len(got) != 0 {
				t.Fatalf("Connector가 실패를 알렸는데 PREVIEW_CLOSE를 보냄: %+v", got)
			}
		})
	}
}

// Connector의 SUCCEEDED는 통지일 뿐이다. 실제 Data WSS attach가 없으면 성공하지 않는다.
func TestCreateDoesNotTrustSucceededWithoutAnAttachedTunnel(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.OpenTimeout = 150 * time.Millisecond })
	f.react(func(open connector.PreviewOpen) {
		f.service.HandlePreviewEvent(connector.PreviewOpenResultEvent{
			ConnectorID: open.ConnectorID, Correlation: open.Correlation, RequestMessageID: "open-message-1",
			Payload: connector.PreviewOpenResultPayload{Outcome: connector.PreviewOutcomeSucceeded},
		})
		// attach하지 않는다.
	})
	_, err := f.create(3000)
	errIs(t, err, ErrOpenTimeout)
	if len(f.gateway.activatedIDs()) != 0 {
		t.Fatal("attach 없는 PreviewSession이 활성화됨")
	}
	// Connector가 TCP forwarding을 열었을 수 있으므로 PREVIEW_CLOSE를 요청한다.
	closes := f.connectors.closeList()
	if len(closes) != 1 || closes[0].Reason != "OPEN_TIMEOUT" || closes[0].ConnectorID != connectorID ||
		closes[0].Correlation.PreviewSessionID != f.gateway.lastExpected(t).SessionID {
		t.Fatalf("PREVIEW_CLOSE = %+v", closes)
	}
	if len(f.gateway.forgotIDs()) != 1 {
		t.Fatalf("Forget = %v", f.gateway.forgotIDs())
	}
}

func TestCreateTimesOutWhenTheConnectorNeverResponds(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.OpenTimeout = 100 * time.Millisecond })
	f.react(nil)
	start := time.Now()
	_, err := f.create(3000)
	errIs(t, err, ErrOpenTimeout)
	if time.Since(start) > 3*time.Second {
		t.Fatalf("시간 초과가 %v 뒤에야 동작함", time.Since(start))
	}
	if closes := f.connectors.closeList(); len(closes) != 1 || closes[0].Reason != "OPEN_TIMEOUT" {
		t.Fatalf("PREVIEW_CLOSE = %+v", closes)
	}
}

// attach 전에 PreviewSession이 끝나면(Credential revoke, Control Session 교체, 서비스 종료) 성공하지 않는다.
func TestCreateFailsWhenTheSessionEndsBeforeAttach(t *testing.T) {
	f := newFixture(t)
	f.react(func(open connector.PreviewOpen) { f.gateway.end(open.Correlation.PreviewSessionID) })
	_, err := f.create(3000)
	errIs(t, err, ErrOpenFailed)
	if len(f.gateway.activatedIDs()) != 0 || len(f.gateway.forgotIDs()) != 1 {
		t.Fatal("끝난 PreviewSession을 정리하지 않음")
	}
}

func TestCreateMapsConnectorSendFailures(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		want      error
		wantClose bool
	}{
		{"Connector 미연결", connector.ErrNotConnected, ErrConnectorUnavailable, false},
		{"HELLO_ACK 전", connector.ErrNotReady, ErrConnectorUnavailable, false},
		{"connection 종료 중", connector.ErrConnectionClosing, ErrConnectorUnavailable, false},
		// preview-v1을 선언하지 않은 Connector에는 Preview message를 보내지 않는다. 연결 단절과 구분한다.
		{"preview-v1 미선언", connector.ErrCapabilityUnsupported, ErrTransportUnsupported, false},
		// 전송 여부가 불명확하다. Connector가 OPEN을 받았을 수 있으므로 CLOSE를 요청한다.
		{"전송 여부 불명확", connector.ErrSendFailed, ErrConnectorUnavailable, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.connectors.openErr = tc.err
			_, err := f.create(3000)
			errIs(t, err, tc.want)
			if tc.want == ErrTransportUnsupported && errors.Is(err, ErrConnectorUnavailable) {
				t.Fatal("capability 미선언을 Connector 단절로 보고함")
			}
			closes := f.connectors.closeList()
			if tc.wantClose != (len(closes) == 1) {
				t.Fatalf("PREVIEW_CLOSE = %+v, wantClose = %v", closes, tc.wantClose)
			}
			// 어떤 경우에도 PreviewSession을 남기지 않고 자동으로 다시 보내지 않는다.
			if len(f.gateway.forgotIDs()) != 1 || f.connectors.openCount() != 0 || len(f.gateway.activatedIDs()) != 0 {
				t.Fatal("정리가 잘못됨")
			}
		})
	}
}

func TestCreateFailsWhenThePreviewSessionCannotBeBoundToTheControlSession(t *testing.T) {
	f := newFixture(t)
	f.gateway.bindErr = preview.ErrControlMismatch
	_, err := f.create(3000)
	errIs(t, err, ErrOpenFailed)
	if f.connectors.openCount() != 0 {
		t.Fatal("묶지 못했는데 PREVIEW_OPEN을 보냄")
	}
	if len(f.gateway.forgotIDs()) != 1 {
		t.Fatal("PreviewSession을 정리하지 않음")
	}
}

func TestCreateFailsWhenTheGatewayIsClosing(t *testing.T) {
	f := newFixture(t)
	f.gateway.expectErr = preview.ErrClosed
	_, err := f.create(3000)
	errIs(t, err, ErrUnavailable)
	f.assertNoSideEffectsBeyondExpect()
}

func (f *fixture) assertNoSideEffectsBeyondExpect() {
	f.t.Helper()
	if f.connectors.calls() != 0 {
		f.t.Fatalf("Connector에 message %d개를 보냄", f.connectors.calls())
	}
}

// 요청이 취소되어도 정리는 끝까지 수행한다(취소된 context를 정리에 쓰지 않는다).
func TestCanceledRequestStillCleansUpAndNotifiesTheConnector(t *testing.T) {
	f := newFixture(t)
	f.react(nil) // Connector가 응답하지 않는다.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := f.service.Create(ctx, owner, CreateInput{LabInstanceID: labID.String(), TargetPort: 3000, RequestID: "request-1"})
		done <- err
	}()
	eventually(t, "PREVIEW_OPEN 전송", func() bool { return f.connectors.openCount() == 1 })
	cancel()
	select {
	case err := <-done:
		errIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("취소된 요청이 끝나지 않음")
	}
	closes := f.connectors.closeList()
	if len(closes) != 1 || closes[0].Reason != "REQUEST_CANCELED" {
		t.Fatalf("PREVIEW_CLOSE = %+v", closes)
	}
	if len(f.gateway.forgotIDs()) != 1 || len(f.gateway.activatedIDs()) != 0 {
		t.Fatal("취소된 요청의 PreviewSession이 남음")
	}
}

// ---- Reset race ----

// PreviewSession을 만드는 동안 generation이 바뀌었거나 READY를 벗어나면 오래된 target을 성공으로 돌려주지 않는다.
func TestCreateDoesNotSucceedWhenTheTargetChangedWhileOpening(t *testing.T) {
	cases := []struct {
		name string
		now  func(*repository.LabInstance)
		err  error
	}{
		{"generation 증가(Reset)", func(l *repository.LabInstance) { l.Generation = 4 }, nil},
		{"generation 감소", func(l *repository.LabInstance) { l.Generation = 2 }, nil},
		{"READY를 벗어남", func(l *repository.LabInstance) { l.Status = "RESETTING" }, nil},
		{"삭제됨", nil, repository.ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			// Connector가 attach하는 사이에 Reset이 generation을 바꾼다.
			base := f.connectors.onOpen
			f.react(func(open connector.PreviewOpen) {
				if tc.now != nil {
					changed := f.store.lab
					tc.now(&changed)
					f.store.setCurrent(&changed)
				} else {
					f.store.mu.Lock()
					f.store.currentErr = tc.err
					f.store.mu.Unlock()
				}
				base(open)
			})
			_, err := f.create(3000)
			errIs(t, err, ErrTargetChanged)
			if len(f.gateway.activatedIDs()) != 0 {
				t.Fatal("오래된 target의 PreviewSession이 활성화됨")
			}
			// Connector가 이미 TCP forwarding을 열었으므로 정리를 요청한다.
			closes := f.connectors.closeList()
			if len(closes) != 1 || closes[0].Reason != "LAB_RESET" {
				t.Fatalf("PREVIEW_CLOSE = %+v", closes)
			}
			if len(f.gateway.forgotIDs()) != 1 {
				t.Fatal("PreviewSession을 정리하지 않음")
			}
		})
	}
}

func TestCreateReportsARecheckFailureWithoutSucceeding(t *testing.T) {
	f := newFixture(t)
	f.store.mu.Lock()
	f.store.currentErr = errors.New("database is down")
	f.store.mu.Unlock()
	_, err := f.create(3000)
	if err == nil || errors.Is(err, ErrTargetChanged) {
		t.Fatalf("error = %v, want 재확인 실패(Target 변경이 아님)", err)
	}
	if len(f.gateway.activatedIDs()) != 0 {
		t.Fatal("재확인에 실패했는데 활성화함")
	}
	if strings.Contains(f.logs.String(), "database is down") {
		t.Fatal("저장소 오류 원문을 기록함")
	}
}

func TestCreateFailsWhenActivationFails(t *testing.T) {
	f := newFixture(t)
	f.gateway.activateErr = preview.ErrSessionEnded
	_, err := f.create(3000)
	errIs(t, err, ErrOpenFailed)
	if closes := f.connectors.closeList(); len(closes) != 1 {
		t.Fatalf("PREVIEW_CLOSE = %+v", closes)
	}
}

// ---- 종료 ----

func (f *fixture) seedSession(id string, user uuid.UUID, org uuid.UUID, ended bool) {
	f.gateway.mu.Lock()
	defer f.gateway.mu.Unlock()
	f.gateway.infos[id] = preview.Info{SessionID: id, OwnerID: user.String(), OrganizationID: org.String(), Ended: ended, LabInstanceID: labID.String()}
}

func TestCloseTerminatesTheOwnersSessionAndIsIdempotent(t *testing.T) {
	f := newFixture(t)
	id := uuid.NewString()
	f.seedSession(id, ownerID, orgID, false)

	if err := f.service.Close(context.Background(), owner, id); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	f.gateway.mu.Lock()
	got := append([]terminatedSession(nil), f.gateway.terminated...)
	f.gateway.mu.Unlock()
	if len(got) != 1 || got[0].id != id || got[0].end != (preview.End{Reason: preview.EndSessionClosed, NotifyConnector: true}) {
		t.Fatalf("Terminate = %+v", got)
	}
	// 이미 종료된 PreviewSession에 대한 반복 요청도 성공이다.
	if err := f.service.Close(context.Background(), owner, id); err != nil {
		t.Fatalf("반복 Close() error = %v", err)
	}
	f.gateway.mu.Lock()
	n := len(f.gateway.terminated)
	f.gateway.mu.Unlock()
	if n != 1 {
		t.Fatalf("반복 Close가 Terminate를 %d번 호출함", n)
	}
}

func TestCloseRejectsOtherUsersAndUnknownSessions(t *testing.T) {
	f := newFixture(t)
	id := uuid.NewString()
	f.seedSession(id, ownerID, orgID, false)

	otherUser := repository.User{ID: otherID, OrganizationID: orgID, OrganizationRole: repository.OrganizationRoleMember}
	admin := repository.User{ID: otherID, OrganizationID: orgID, OrganizationRole: repository.OrganizationRoleAdmin}
	foreignOrg := repository.User{ID: ownerID, OrganizationID: otherOrgID, OrganizationRole: repository.OrganizationRoleMember}
	for name, user := range map[string]repository.User{"다른 사용자": otherUser, "Organization ADMIN": admin, "다른 Organization": foreignOrg} {
		errIs(t, f.service.Close(context.Background(), user, id), ErrForbidden)
		_ = name
	}
	errIs(t, f.service.Close(context.Background(), owner, uuid.NewString()), ErrNotFound)
	errIs(t, f.service.Close(context.Background(), owner, "not-a-uuid"), ErrNotFound)
	errIs(t, f.service.Close(context.Background(), owner, strings.ToUpper(id)), ErrNotFound)

	f.gateway.mu.Lock()
	n := len(f.gateway.terminated)
	f.gateway.mu.Unlock()
	if n != 0 {
		t.Fatalf("거절된 Close가 Terminate를 호출함")
	}
}

func TestSessionEndedNotifiesTheConnectorOnlyForServerInitiatedEnds(t *testing.T) {
	f := newFixture(t)
	base := preview.Ended{
		SessionID: "preview-1", ConnectorID: connectorID, LabInstanceID: labID.String(), Generation: 3, RequestID: "request-1",
	}

	notify := base
	notify.Reason, notify.NotifyConnector = preview.EndSessionExpired, true
	f.service.SessionEnded(notify)
	closes := f.connectors.closeList()
	if len(closes) != 1 || closes[0].Reason != "SESSION_EXPIRED" || closes[0].ConnectorID != connectorID || closes[0].RequestID != "request-1" ||
		closes[0].Correlation != (connector.PreviewCorrelation{PreviewSessionID: "preview-1", LabInstanceID: labID.String(), Generation: 3}) {
		t.Fatalf("PREVIEW_CLOSE = %+v", closes)
	}

	// Connector가 이미 알거나 Control이 끊겨 보낼 수 없는 종료는 PREVIEW_CLOSE를 보내지 않는다.
	for _, reason := range []string{preview.EndTunnelClosed, preview.EndCredentialRevoked, preview.EndControlReplaced} {
		quiet := base
		quiet.Reason = reason
		f.service.SessionEnded(quiet)
	}
	if got := f.connectors.closeList(); len(got) != 1 {
		t.Fatalf("PREVIEW_CLOSE %d개, want 1개", len(got))
	}

	// Connector를 사용할 수 없어도 panic하거나 오류를 전파하지 않는다.
	f.connectors.mu.Lock()
	f.connectors.closeErr = connector.ErrNotConnected
	f.connectors.mu.Unlock()
	f.service.SessionEnded(notify)
	if !strings.Contains(f.logs.String(), "connector_unavailable") {
		t.Fatalf("PREVIEW_CLOSE를 보내지 못한 사유가 기록되지 않음: %s", f.logs.String())
	}
}

func TestCloseForLabMutationEndsOnlyThatLabsLiveSessions(t *testing.T) {
	f := newFixture(t)
	a, b, other, ended := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, id := range []string{a, b, ended} {
		f.seedSession(id, ownerID, orgID, id == ended)
	}
	f.seedSession(other, ownerID, orgID, false)
	f.gateway.mu.Lock()
	f.gateway.labSessions[labID.String()] = []string{a, b, ended}
	f.gateway.labSessions["other-lab"] = []string{other}
	f.gateway.mu.Unlock()

	n, err := f.service.CloseForLabMutation(context.Background(), LabMutation{LabInstanceID: labID, Reason: preview.EndLabReset})
	if err != nil || n != 2 {
		t.Fatalf("CloseForLabMutation() = %d, %v, want 2, nil", n, err)
	}
	f.gateway.mu.Lock()
	defer f.gateway.mu.Unlock()
	for _, term := range f.gateway.terminated {
		if term.end != (preview.End{Reason: preview.EndLabReset, NotifyConnector: true}) || term.id == other {
			t.Fatalf("Terminate = %+v", term)
		}
	}
	if len(f.gateway.terminated) != 2 {
		t.Fatalf("Terminate %d번, want 2번", len(f.gateway.terminated))
	}
}

func TestCloseForLabMutationRequiresAResetOrCleanupReason(t *testing.T) {
	f := newFixture(t)
	for _, reason := range []string{"", preview.EndSessionClosed, "RESET", "lab_reset"} {
		if _, err := f.service.CloseForLabMutation(context.Background(), LabMutation{LabInstanceID: labID, Reason: reason}); !errors.Is(err, ErrInvalidReason) {
			t.Errorf("reason %q error = %v, want ErrInvalidReason", reason, err)
		}
	}
	if _, err := f.service.CloseForLabMutation(context.Background(), LabMutation{LabInstanceID: labID, Reason: preview.EndLabCleanup}); err != nil {
		t.Errorf("LAB_CLEANUP error = %v", err)
	}
}

// ---- 종료와 이벤트 ----

func TestShutdownRejectsNewCreatesAndWaitsForInFlightOnes(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.OpenTimeout = time.Minute })
	f.react(nil) // 진행 중인 생성이 끝나지 않는다.
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_, err := f.service.Create(ctx, owner, CreateInput{LabInstanceID: labID.String(), TargetPort: 3000})
		done <- err
	}()
	eventually(t, "PREVIEW_OPEN 전송", func() bool { return f.connectors.openCount() == 1 })

	shut := make(chan error, 1)
	go func() {
		sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer scancel()
		shut <- f.service.Shutdown(sctx)
	}()
	eventually(t, "종료 표시", func() bool {
		f.service.mu.Lock()
		defer f.service.mu.Unlock()
		return f.service.closed
	})

	// 종료가 시작된 뒤의 새 생성은 저장소나 Connector를 건드리지 않고 거절한다.
	opensBefore := f.connectors.openCount()
	if _, err := f.create(3000); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("종료 중 Create() error = %v, want ErrUnavailable", err)
	}
	if f.connectors.openCount() != opensBefore {
		t.Fatal("종료 중 생성이 PREVIEW_OPEN을 보냄")
	}

	select {
	case err := <-shut:
		t.Fatalf("진행 중인 생성이 있는데 Shutdown이 끝남: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	// 진행 중인 생성이 끝나면 Shutdown도 끝난다.
	cancel()
	<-done
	if err := <-shut; err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestHandlePreviewEventIgnoresUnmatchedAndLateResults(t *testing.T) {
	f := newFixture(t)
	f.service.HandlePreviewEvent(connector.PreviewUnmatchedEvent{ConnectorID: connectorID, MessageType: "PREVIEW_OPEN_RESULT", Reason: connector.ReasonNoPending})
	// 기다리는 Create가 없는 결과(시간 초과 뒤 늦게 온 것)는 아무것도 하지 않는다.
	f.service.HandlePreviewEvent(connector.PreviewOpenResultEvent{
		ConnectorID: connectorID, Correlation: connector.PreviewCorrelation{PreviewSessionID: "gone", LabInstanceID: "lab", Generation: 1},
		Payload: connector.PreviewOpenResultPayload{Outcome: connector.PreviewOutcomeFailed},
	})
	if f.connectors.calls() != 0 {
		t.Fatal("무관한 event가 Connector 호출을 만듦")
	}
}

// Connector가 보낸 오류 문구/code 원문은 log에 남기지 않는다. 안전한 분류만 남는다.
func TestConnectorErrorTextIsNeverLogged(t *testing.T) {
	f := newFixture(t)
	f.react(f.fail("SECRET-RAW-CONNECTOR-ERROR /home/student/private-project"))
	_, err := f.create(3000)
	errIs(t, err, ErrOpenFailed)
	logs := f.logs.String()
	if strings.Contains(logs, "SECRET-RAW-CONNECTOR-ERROR") || strings.Contains(logs, "private-project") {
		t.Fatalf("Connector 오류 원문이 log에 남음: %s", logs)
	}
	if !strings.Contains(logs, "UNKNOWN") {
		t.Fatalf("안전한 분류가 log에 없음: %s", logs)
	}
}

func TestNewServiceValidatesItsOptions(t *testing.T) {
	good := Options{Store: newStore(), Connectors: &fakeConnectors{}, Gateway: newGateway(), Policy: mustPolicy(t, "3000"), TTL: time.Hour}
	if _, err := NewService(good); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Options){
		"Store 없음":       func(o *Options) { o.Store = nil },
		"Connectors 없음":  func(o *Options) { o.Connectors = nil },
		"Gateway 없음":     func(o *Options) { o.Gateway = nil },
		"TTL 없음(기본값 없음)": func(o *Options) { o.TTL = 0 },
		"TTL 음수":         func(o *Options) { o.TTL = -time.Second },
		"OpenTimeout 음수": func(o *Options) { o.OpenTimeout = -time.Second },
	} {
		o := good
		mutate(&o)
		if _, err := NewService(o); err == nil {
			t.Errorf("%s: NewService()가 오류 없이 통과함", name)
		}
	}
}

func TestOpenTunnelSucceedsAndCorrelatesWithOpenMessageID(t *testing.T) {
	f := newFixture(t)
	sessID := uuid.NewString()
	f.gateway.infos[sessID] = preview.Info{
		SessionID:        sessID,
		LabInstanceID:    labID.String(),
		Generation:       3,
		ConnectorID:      connectorID,
		TargetVMKey:      "vk-web",
		ProviderServerID: "srv-web-g3",
		TargetPort:       5173,
		ExpiresAt:        time.Now().Add(time.Hour),
	}
	f.gateway.sessions[sessID] = &fakeSession{attached: make(chan struct{}), ended: make(chan struct{})}

	go func() {
		eventually(t, "PREVIEW_OPEN 전송", func() bool { return f.connectors.openCount() == 1 })
		f.gateway.attach(sessID)
	}()

	err := f.service.OpenTunnel(context.Background(), sessID)
	if err != nil {
		t.Fatalf("OpenTunnel() error = %v", err)
	}

	if f.connectors.openCount() != 1 {
		t.Fatalf("openCount = %d, want 1", f.connectors.openCount())
	}
	open := f.connectors.opens[0]
	if open.MessageID == "" {
		t.Fatal("open.MessageID가 비어 있음")
	}
	if open.Correlation.PreviewSessionID != sessID || open.Correlation.Generation != 3 {
		t.Fatalf("correlation 불일치: %+v", open.Correlation)
	}
}

func TestOpenTunnelRechecksLabTargetAndTerminatesOnDrift(t *testing.T) {
	f := newFixture(t)
	sessID := uuid.NewString()
	f.gateway.infos[sessID] = preview.Info{
		SessionID:        sessID,
		LabInstanceID:    labID.String(),
		Generation:       3,
		ConnectorID:      connectorID,
		TargetVMKey:      "vk-web",
		ProviderServerID: "srv-web-g3",
		TargetPort:       5173,
		ExpiresAt:        time.Now().Add(time.Hour),
	}

	drifted := f.store.lab
	drifted.Generation = 4
	f.store.current = &drifted

	err := f.service.OpenTunnel(context.Background(), sessID)
	if !errors.Is(err, ErrTargetChanged) {
		t.Fatalf("OpenTunnel() error = %v, want ErrTargetChanged", err)
	}

	if len(f.gateway.terminated) != 1 || f.gateway.terminated[0].end.Reason != preview.EndLabReset {
		t.Fatalf("terminated = %+v", f.gateway.terminated)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("시간 안에 %s 조건이 만족되지 않음", what)
}
