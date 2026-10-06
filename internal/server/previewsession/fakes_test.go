package previewsession

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/preview"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

var (
	orgID       = uuid.MustParse("00000000-0000-4000-8000-0000000000a1")
	classID     = uuid.MustParse("00000000-0000-4000-8000-0000000000b1")
	ownerID     = uuid.MustParse("00000000-0000-4000-8000-000000000001")
	otherID     = uuid.MustParse("00000000-0000-4000-8000-000000000002")
	otherOrgID  = uuid.MustParse("00000000-0000-4000-8000-0000000000a2")
	labID       = uuid.MustParse("00000000-0000-4000-8000-0000000000d1")
	connectorID = uuid.MustParse("00000000-0000-4000-8000-0000000000c1")
	controlID   = uuid.MustParse("00000000-0000-4000-8000-0000000000e1")
	credID      = uuid.MustParse("00000000-0000-4000-8000-0000000000f1")
	owner       = repository.User{ID: ownerID, OrganizationID: orgID, OrganizationRole: repository.OrganizationRoleMember}
)

// fakeStore는 resolve의 판정만 검증하기 위한 in-memory Store다. 구현하지 않은 method는 호출되면 nil embed로 panic한다.
type fakeStore struct {
	repository.Repositories

	mu           sync.Mutex
	lab          repository.LabInstance
	labErr       error
	membership   repository.ClassMembership
	memberErr    error
	snapshot     repository.CreationSnapshotTargets
	snapshotErr  error
	connectorErr error
	servers      map[string][]repository.ProviderServer
	serversErr   error

	// current는 LabInstanceByID(외부 I/O 뒤 재확인)가 반환하는 LabInstance다. nil이면 lab이다.
	current    *repository.LabInstance
	currentErr error

	inTransaction bool
	transactions  int
	calls         []string
	serverQueries []serverQuery
}

type serverQuery struct {
	generation int64
	name       string
}

func (f *fakeStore) WithinTransaction(ctx context.Context, fn func(context.Context, repository.Repositories) error) error {
	f.mu.Lock()
	f.transactions++
	f.inTransaction = true
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inTransaction = false
		f.mu.Unlock()
	}()
	return fn(ctx, f)
}

func (f *fakeStore) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeStore) LabInstanceForShare(context.Context, uuid.UUID) (repository.LabInstance, error) {
	f.record("LabInstanceForShare")
	return f.lab, f.labErr
}

func (f *fakeStore) LabInstanceByID(context.Context, uuid.UUID) (repository.LabInstance, error) {
	f.record("LabInstanceByID")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.currentErr != nil {
		return repository.LabInstance{}, f.currentErr
	}
	if f.current != nil {
		return *f.current, nil
	}
	return f.lab, nil
}

func (f *fakeStore) ClassMembership(context.Context, uuid.UUID, uuid.UUID) (repository.ClassMembership, error) {
	f.record("ClassMembership")
	return f.membership, f.memberErr
}

func (f *fakeStore) CreationSnapshotTargets(context.Context, uuid.UUID) (repository.CreationSnapshotTargets, error) {
	f.record("CreationSnapshotTargets")
	return f.snapshot, f.snapshotErr
}

func (f *fakeStore) ConnectorIDForLabInstance(context.Context, uuid.UUID) (uuid.UUID, error) {
	f.record("ConnectorIDForLabInstance")
	if f.connectorErr != nil {
		return uuid.UUID{}, f.connectorErr
	}
	return connectorID, nil
}

func (f *fakeStore) ProviderServers(_ context.Context, _ uuid.UUID, generation int64, logicalName string) ([]repository.ProviderServer, error) {
	f.record("ProviderServers")
	f.mu.Lock()
	f.serverQueries = append(f.serverQueries, serverQuery{generation: generation, name: logicalName})
	f.mu.Unlock()
	return f.servers[logicalName], f.serversErr
}

func (f *fakeStore) insideTransaction() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inTransaction
}

func (f *fakeStore) setCurrent(lab *repository.LabInstance) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.current = lab
}

func newStore() *fakeStore {
	return &fakeStore{
		lab: repository.LabInstance{
			ID: labID, OrganizationID: orgID, LabExecutionID: uuid.New(), ClassID: classID, UserID: ownerID,
			Status: "READY", Generation: 3,
		},
		membership: repository.ClassMembership{ClassID: classID, UserID: ownerID, OrganizationID: orgID, Role: repository.ClassRoleStudent},
		snapshot: repository.CreationSnapshotTargets{
			WorkspaceVMKey: "vk-web",
			VMs:            []repository.SnapshotVM{{VMKey: "vk-web"}, {VMKey: "vk-db"}},
		},
		servers: map[string][]repository.ProviderServer{
			"vk-web": {{ID: uuid.New(), ProviderID: "srv-web-g3", LifecycleStatus: "PRESENT"}},
			"vk-db":  {{ID: uuid.New(), ProviderID: "srv-db-g3", LifecycleStatus: "PRESENT"}},
		},
	}
}

// fakeGateway는 Gateway 경계의 호출을 기록하고 생성 중인 PreviewSession의 상태 channel을 test가 직접 움직이게 한다.
type fakeGateway struct {
	mu          sync.Mutex
	expected    []preview.Expected
	bound       map[string]preview.Binding
	forgot      []string
	terminated  []terminatedSession
	activated   []string
	sessions    map[string]*fakeSession
	infos       map[string]preview.Info
	labSessions map[string][]string

	expectErr   error
	bindErr     error
	activateErr error
	activation  preview.Activation
}

type fakeSession struct {
	attached chan struct{}
	ended    chan struct{}
}

type terminatedSession struct {
	id  string
	end preview.End
}

func newGateway() *fakeGateway {
	return &fakeGateway{
		bound: map[string]preview.Binding{}, sessions: map[string]*fakeSession{}, infos: map[string]preview.Info{},
		labSessions: map[string][]string{},
		activation:  preview.Activation{URL: "https://id.preview.example.test/__labbit/bootstrap#CREDENTIAL", ExpiresAt: time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)},
	}
}

func (g *fakeGateway) Expect(e preview.Expected) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.expectErr != nil {
		return g.expectErr
	}
	g.expected = append(g.expected, e)
	g.sessions[e.SessionID] = &fakeSession{attached: make(chan struct{}), ended: make(chan struct{})}
	return nil
}

func (g *fakeGateway) Bind(id string, b preview.Binding) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.bindErr != nil {
		return g.bindErr
	}
	g.bound[id] = b
	return nil
}

func (g *fakeGateway) Pending(id string) (<-chan struct{}, <-chan struct{}, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.sessions[id]
	if s == nil {
		return nil, nil, false
	}
	return s.attached, s.ended, true
}

func (g *fakeGateway) Activate(id string) (preview.Activation, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.activateErr != nil {
		return preview.Activation{}, g.activateErr
	}
	g.activated = append(g.activated, id)
	return g.activation, nil
}

func (g *fakeGateway) Forget(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.forgot = append(g.forgot, id)
}

func (g *fakeGateway) Terminate(id string, end preview.End) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	info, ok := g.infos[id]
	if !ok || info.Ended {
		return false
	}
	info.Ended = true
	info.EndReason = end.Reason
	g.infos[id] = info
	g.terminated = append(g.terminated, terminatedSession{id, end})
	return true
}

func (g *fakeGateway) Info(id string) (preview.Info, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	info, ok := g.infos[id]
	return info, ok
}

func (g *fakeGateway) SessionsForLab(labInstanceID string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, id := range g.labSessions[labInstanceID] {
		if !g.infos[id].Ended {
			out = append(out, id)
		}
	}
	return out
}

func (g *fakeGateway) attach(id string) {
	g.mu.Lock()
	s := g.sessions[id]
	g.mu.Unlock()
	if s != nil {
		close(s.attached)
	}
}

func (g *fakeGateway) end(id string) {
	g.mu.Lock()
	s := g.sessions[id]
	g.mu.Unlock()
	if s != nil {
		close(s.ended)
	}
}

func (g *fakeGateway) lastExpected(t *testing.T) preview.Expected {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.expected) == 0 {
		t.Fatal("Gateway에 등록된 PreviewSession이 없음")
	}
	return g.expected[len(g.expected)-1]
}

func (g *fakeGateway) expectedCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.expected)
}

func (g *fakeGateway) forgotIDs() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.forgot...)
}

func (g *fakeGateway) activatedIDs() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.activated...)
}

// fakeConnectors는 Connector Control 경계의 호출을 기록하고 Connector의 반응을 test가 정한다.
type fakeConnectors struct {
	store *fakeStore

	mu        sync.Mutex
	opens     []connector.PreviewOpen
	closes    []connector.PreviewClose
	forgotten []string
	openErr   error
	closeErr  error
	session   connector.Session
	// onOpen은 PREVIEW_OPEN을 보낸 직후 비동기로 호출되는 Connector의 반응이다.
	onOpen func(connector.PreviewOpen)
	// wasInTransaction은 PREVIEW_OPEN을 보낼 때 DB transaction이 열려 있었는지 기록한다.
	wasInTransaction bool
}

func (c *fakeConnectors) SendPreviewOpen(_ context.Context, open connector.PreviewOpen) (connector.SentMessage, error) {
	c.mu.Lock()
	err := c.openErr
	c.wasInTransaction = c.wasInTransaction || (c.store != nil && c.store.insideTransaction())
	session, onOpen := c.session, c.onOpen
	c.mu.Unlock()
	if err != nil {
		return connector.SentMessage{}, err
	}
	if open.OnRoute != nil {
		if err := open.OnRoute(session); err != nil {
			return connector.SentMessage{}, err
		}
	}
	c.mu.Lock()
	c.opens = append(c.opens, open)
	c.mu.Unlock()
	if onOpen != nil {
		go onOpen(open)
	}
	return connector.SentMessage{MessageID: "open-message-1"}, nil
}

func (c *fakeConnectors) SendPreviewClose(_ context.Context, cl connector.PreviewClose) (connector.SentMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closeErr != nil {
		return connector.SentMessage{}, c.closeErr
	}
	c.closes = append(c.closes, cl)
	return connector.SentMessage{MessageID: "close-message-1"}, nil
}

func (c *fakeConnectors) ForgetPreviewOpen(connectorID uuid.UUID, id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.forgotten = append(c.forgotten, id)
	return true
}

func (c *fakeConnectors) openCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.opens)
}

func (c *fakeConnectors) closeList() []connector.PreviewClose {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]connector.PreviewClose(nil), c.closes...)
}

func (c *fakeConnectors) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.opens) + len(c.closes)
}

// lockedBuffer는 동시에 써도 되는 log 출력이다.
type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fixture는 Service와 fake 경계를 묶는다.
type fixture struct {
	t          *testing.T
	store      *fakeStore
	gateway    *fakeGateway
	connectors *fakeConnectors
	service    *Service
	logs       *lockedBuffer
}

type fixtureOption func(*Options)

func newFixture(t *testing.T, opts ...fixtureOption) *fixture {
	t.Helper()
	f := &fixture{t: t, store: newStore(), gateway: newGateway(), logs: &lockedBuffer{}}
	f.connectors = &fakeConnectors{store: f.store, session: connector.Session{ID: controlID, ConnectorID: connectorID, CredentialID: credID}}
	o := Options{
		Store: f.store, Connectors: f.connectors, Gateway: f.gateway, Policy: mustPolicy(t, "3000,5173,80"), TTL: time.Hour,
		Logger:      slog.New(slog.NewJSONHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		OpenTimeout: 2 * time.Second,
	}
	for _, opt := range opts {
		opt(&o)
	}
	var err error
	f.service, err = NewService(o)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	// Connector의 기본 반응: PREVIEW_OPEN_RESULT SUCCEEDED를 알리고 Data WSS를 attach한다.
	f.connectors.onOpen = func(open connector.PreviewOpen) {
		f.service.HandlePreviewEvent(connector.PreviewOpenResultEvent{
			ConnectorID: open.ConnectorID, Correlation: open.Correlation, RequestMessageID: "open-message-1", RequestID: open.RequestID,
			Payload: connector.PreviewOpenResultPayload{Outcome: connector.PreviewOutcomeSucceeded},
		})
		f.gateway.attach(open.Correlation.PreviewSessionID)
	}
	return f
}

// react는 Connector의 반응을 바꾼다.
func (f *fixture) react(fn func(connector.PreviewOpen)) {
	f.connectors.mu.Lock()
	defer f.connectors.mu.Unlock()
	f.connectors.onOpen = fn
}

// fail은 Connector가 PREVIEW_OPEN_RESULT FAILED를 알리는 반응이다.
func (f *fixture) fail(code string) func(connector.PreviewOpen) {
	return func(open connector.PreviewOpen) {
		payload := connector.PreviewOpenResultPayload{Outcome: connector.PreviewOutcomeFailed}
		if code != "" {
			payload.Error = &protocol.SafeError{Code: code}
		}
		f.service.HandlePreviewEvent(connector.PreviewOpenResultEvent{
			ConnectorID: open.ConnectorID, Correlation: open.Correlation, RequestMessageID: "open-message-1", RequestID: open.RequestID, Payload: payload,
		})
	}
}

func (f *fixture) create(port int) (Created, error) {
	f.t.Helper()
	return f.service.Create(context.Background(), owner, CreateInput{LabInstanceID: labID.String(), TargetPort: port, RequestID: "request-1"})
}

func errIs(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}
