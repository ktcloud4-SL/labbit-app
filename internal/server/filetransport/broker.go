// Package filetransport는 Workspace File 요청(Tree/Read/Save)을 고객 환경 Connector로 전달하는 SaaS ↔ Connector transport다
// (contracts/connector/README.md §7a).
//
// persistent Control WSS에는 요청의 lifecycle/correlation(FILE_OPEN/FILE_CLOSE)만 싣고, 경로·디렉터리 목록·파일 본문은 요청마다 Connector가
// outbound로 여는 짧은 File Data WSS 하나로 주고받는다. 1 HTTP File 요청은 1 Data WSS이며 요청 frame 하나와 결과 frame 하나 뒤에 닫는다.
// 복잡한 persistent File multiplexer나 broker를 두지 않는다.
//
// Broker는 workspacefile.Transport를 구현한다. pending 요청은 process 안의 ephemeral 상태이며 PostgreSQL에 저장하지 않는다.
// Connector identity는 Data WSS Upgrade의 Connector Credential 인증 결과가 권위이며 message가 주장하는 값을 신뢰하지 않는다.
// 인증된 Connector, fileRequestId, labInstanceId, generation, Workspace VM 식별이 모두 기대한 값과 같을 때만 attach하며
// 하나라도 다른 frame은 다른 pending 요청을 완료시키지 않는다.
//
// 파일 본문, 디렉터리 목록, 경로는 log, trace, metric에 남기지 않는다. 관측 metadata는 file_request_id, request_id, lab_instance_id,
// connector_id, generation, operation, 안전한 error_code, duration_ms뿐이다.
package filetransport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/workspacefile"
)

const (
	// DataPath는 Connector File Data WSS endpoint다.
	DataPath = "/connector/v1/file-data"

	defaultAttachTimeout    = 10 * time.Second
	defaultOperationTimeout = 30 * time.Second
	// writeTimeout은 frame 하나를 쓰는 데 쓸 수 있는 시간이다.
	writeTimeout = 10 * time.Second
	// controlWriteTimeout은 close frame 같은 control frame을 쓰는 데 쓸 수 있는 시간이다.
	controlWriteTimeout = time.Second
	// cleanupTimeout은 요청이 취소된 뒤에도 끝내야 하는 정리(FILE_CLOSE)의 시간 상한이다.
	cleanupTimeout = 5 * time.Second
)

// FILE_CLOSE의 종료 원인이다(file-control.schema.json).
const (
	closeReasonCanceled   = "REQUEST_CANCELED"
	closeReasonTimeout    = "REQUEST_TIMEOUT"
	closeReasonSendFailed = "OPEN_SEND_FAILED"
	closeReasonRestarting = "SERVICE_RESTARTING"
)

// Control은 Connector Control connection으로 File lifecycle message를 보내는 경계다. *connector.Router가 구현한다.
type Control interface {
	SendFileOpen(ctx context.Context, open connector.FileOpen) (connector.SentMessage, error)
	SendFileClose(ctx context.Context, cl connector.FileClose) (connector.SentMessage, error)
	ForgetFileOpen(connectorID uuid.UUID, fileRequestID string) bool
}

// Authenticator는 File Data WSS의 Connector Credential을 검증하는 use case다. *connector.Service가 구현한다.
// Control WSS와 같은 인증 의미(revoke된 Credential/Connector 거절)를 쓴다.
type Authenticator interface {
	Authenticate(ctx context.Context, credential connector.Credential) (connector.Principal, error)
}

// Options는 Broker 구성이다.
type Options struct {
	Control Control
	Auth    Authenticator
	// Logger가 nil이면 로그를 남기지 않는다. 경로, 파일 본문, Credential은 어떤 경우에도 기록하지 않는다.
	Logger *slog.Logger
	// AttachTimeout은 FILE_OPEN을 보낸 뒤 Connector의 File Data WSS attach를 기다리는 시간이다. 0이면 10초다.
	AttachTimeout time.Duration
	// OperationTimeout은 attach 뒤 요청 frame을 보내고 결과를 받는 데 쓸 수 있는 시간이다. 0이면 30초다.
	OperationTimeout time.Duration
}

// Broker는 workspacefile.Transport를 구현하는 Connector File transport다. connector.FileSink와 connector.RevokeObserver도 구현한다.
type Broker struct {
	control Control
	auth    Authenticator
	logger  *slog.Logger

	attachTimeout time.Duration
	opTimeout     time.Duration

	trust *dataTrust

	mu      sync.Mutex
	closed  bool
	pending map[string]*request
	done    chan struct{}
	// wg는 처리 중인 Data WSS Upgrade 요청과 열린 connection을 센다.
	wg sync.WaitGroup
}

var (
	_ workspacefile.Transport  = (*Broker)(nil)
	_ connector.FileSink       = (*Broker)(nil)
	_ connector.RevokeObserver = (*Broker)(nil)
)

// New는 Broker를 만든다. Router와 Broker는 서로를 필요로 하므로(Router가 결과를 Broker에 넘기고 Broker가 Router로 보낸다)
// 조립하는 쪽이 둘 중 하나를 forwarder로 넘겨 순환을 끊는다(internal/server/app).
func New(opts Options) (*Broker, error) {
	if opts.Control == nil || opts.Auth == nil {
		return nil, errors.New("filetransport: Control과 Auth가 필요합니다")
	}
	b := &Broker{
		control:       opts.Control,
		auth:          opts.Auth,
		logger:        opts.Logger,
		attachTimeout: opts.AttachTimeout,
		opTimeout:     opts.OperationTimeout,
		trust:         newDataTrust(),
		pending:       make(map[string]*request),
		done:          make(chan struct{}),
	}
	if b.logger == nil {
		b.logger = slog.New(slog.DiscardHandler)
	}
	if b.attachTimeout == 0 {
		b.attachTimeout = defaultAttachTimeout
	}
	if b.opTimeout == 0 {
		b.opTimeout = defaultOperationTimeout
	}
	if b.attachTimeout < 0 || b.opTimeout < 0 {
		return nil, errors.New("filetransport: 시간 설정은 0보다 커야 합니다")
	}
	return b, nil
}

// operation은 File 요청의 종류다.
type operation int

const (
	opTree operation = iota + 1
	opRead
	opSave
)

func (o operation) wire() string {
	switch o {
	case opTree:
		return connector.FileOperationTree
	case opRead:
		return connector.FileOperationRead
	default:
		return connector.FileOperationSave
	}
}

// outcome은 요청 하나의 결과다. err가 nil이면 성공이다.
type outcome struct {
	err      error
	entries  []workspacefile.Entry
	data     workspacefile.FileData
	revision workspacefile.Revision
}

// request는 pending File 요청 하나다. 경로와 본문을 담지만 log에 쓰지 않는다.
type request struct {
	id          string
	op          operation
	connectorID uuid.UUID
	// 아래는 서버가 현재 DB 상태에서 결정한 값이며 Connector가 attach에서 같은 값을 돌려줘야 한다.
	labInstanceID string
	generation    int64
	vmKey         string
	serverID      string
	// httpRequestID는 원본 HTTP request의 correlation이다(선택).
	httpRequestID string

	// 요청 frame의 내용이다.
	path     string
	maxBytes int64
	expected string
	content  []byte

	log *slog.Logger

	// attached는 Data WSS가 이 요청에 bind되면 닫힌다.
	attached chan struct{}
	// result는 결과를 한 번만 받는다(용량 1). finish가 처음 호출될 때만 쓴다.
	result chan outcome
	// saveSent는 Save 본문 Binary frame을 쓰기 시작했음을 뜻한다. 그 뒤의 실패는 저장 여부를 알 수 없다.
	saveSent atomic.Bool

	mu       sync.Mutex
	state    requestState
	conn     *dataConn
	attachCh sync.Once
}

type requestState int

const (
	stateWaiting requestState = iota
	stateAttached
	stateFinished
)

// finish는 결과를 기록한다. 처음 호출만 적용하며 적용했으면 true다. 막히지 않는다.
func (r *request) finish(out outcome) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == stateFinished {
		return false
	}
	r.state = stateFinished
	r.result <- out
	return true
}

// claimAttach는 대기 중인 요청을 d에 bind한다. 이미 attach되었거나 끝났다면 false다.
func (r *request) claimAttach(d *dataConn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != stateWaiting {
		return false
	}
	r.state = stateAttached
	r.conn = d
	r.attachCh.Do(func() { close(r.attached) })
	return true
}

// failure는 transport 실패를 요청의 결과로 바꾼다. Save 본문을 보내기 시작한 뒤의 실패는 저장 여부를 알 수 없으므로
// 자동으로 다시 보내지 않는 ErrSaveOutcomeUnknown이다.
func (r *request) failure(err error) outcome {
	if r.op == opSave && r.saveSent.Load() {
		err = workspacefile.ErrSaveOutcomeUnknown
	}
	return outcome{err: err}
}

// shutdown은 요청을 err로 끝내고 bind된 Data WSS가 있으면 닫는다. 여러 번 호출해도 안전하다.
func (r *request) shutdown(err error, code int, text string) {
	r.finish(r.failure(err))
	r.mu.Lock()
	conn := r.conn
	r.mu.Unlock()
	if conn != nil {
		conn.closeNow(code, text)
	}
}

func (b *Broker) newRequest(target workspacefile.Target, op operation) *request {
	id := uuid.NewString()
	r := &request{
		id:            id,
		op:            op,
		connectorID:   target.ConnectorID,
		labInstanceID: target.LabInstanceID.String(),
		generation:    target.Generation,
		vmKey:         target.WorkspaceVMKey,
		serverID:      target.ProviderServerID,
		httpRequestID: target.RequestID,
		attached:      make(chan struct{}),
		result:        make(chan outcome, 1),
	}
	r.log = b.logger.With(
		"file_request_id", id,
		"lab_instance_id", r.labInstanceID,
		"connector_id", target.ConnectorID.String(),
		"generation", target.Generation,
		"request_id", target.RequestID,
		"operation", op.wire(),
	)
	return r
}

// Tree는 workspacefile.Transport의 구현이다.
func (b *Broker) Tree(ctx context.Context, target workspacefile.Target, dir workspacefile.Path) ([]workspacefile.Entry, error) {
	req := b.newRequest(target, opTree)
	req.path = dir.String()
	out, err := b.run(ctx, req)
	if err != nil {
		return nil, err
	}
	return out.entries, nil
}

// Read는 workspacefile.Transport의 구현이다.
func (b *Broker) Read(ctx context.Context, target workspacefile.Target, file workspacefile.Path, maxBytes int64) (workspacefile.FileData, error) {
	if maxBytes < 1 {
		return workspacefile.FileData{}, errors.New("filetransport: maxBytes는 1 이상이어야 합니다")
	}
	req := b.newRequest(target, opRead)
	req.path = file.String()
	req.maxBytes = maxBytes
	out, err := b.run(ctx, req)
	if err != nil {
		return workspacefile.FileData{}, err
	}
	return out.data, nil
}

// Save는 workspacefile.Transport의 구현이다. 요청을 보낸 뒤 결과를 받지 못해도 자동으로 다시 보내지 않는다.
func (b *Broker) Save(ctx context.Context, target workspacefile.Target, file workspacefile.Path, expected workspacefile.Revision, content []byte) (workspacefile.Revision, error) {
	req := b.newRequest(target, opSave)
	req.path = file.String()
	req.expected = string(expected)
	req.content = content
	out, err := b.run(ctx, req)
	if err != nil {
		return "", err
	}
	return out.revision, nil
}

// run은 FILE_OPEN을 보내고 Data WSS attach와 결과를 기다린다. 어떤 반환에서도 요청의 pending 상태와 Data WSS를 정리한다.
func (b *Broker) run(ctx context.Context, req *request) (outcome, error) {
	start := time.Now()
	if !b.register(req) {
		req.log.Warn("File 요청 거절", "error_code", "SERVICE_SHUTTING_DOWN")
		return outcome{}, workspacefile.ErrTransportUnavailable
	}
	defer b.release(req)

	_, err := b.control.SendFileOpen(ctx, connector.FileOpen{
		ConnectorID:      req.connectorID,
		RequestID:        req.httpRequestID,
		Correlation:      connector.FileCorrelation{FileRequestID: req.id, LabInstanceID: req.labInstanceID, Generation: req.generation},
		Operation:        req.op.wire(),
		TargetVMKey:      req.vmKey,
		ProviderServerID: req.serverID,
		Trace:            connector.TraceFromContext(ctx),
	})
	switch {
	case err == nil:
	case errors.Is(err, connector.ErrCapabilityUnsupported):
		// Connector는 연결되어 있지만 file-v1을 선언하지 않았다. FILE_OPEN을 보내지 않았다. 버전으로 추론하지 않는다.
		req.log.Warn("File 요청 실패", "error_code", "CAPABILITY_UNSUPPORTED", "duration_ms", since(start))
		return outcome{}, workspacefile.ErrTransportUnavailable
	case errors.Is(err, connector.ErrConnectorUnavailable):
		// 아무것도 쓰지 않았고 pending도 없다.
		req.log.Warn("File 요청 실패", "error_code", "CONNECTOR_UNAVAILABLE", "duration_ms", since(start))
		return outcome{}, workspacefile.ErrConnectorUnavailable
	case errors.Is(err, connector.ErrSendFailed):
		// 전송 여부가 불명확하다. Connector가 FILE_OPEN을 받았을 수 있으므로 FILE_CLOSE를 요청한다.
		req.log.Warn("File 요청 실패", "error_code", "OPEN_SEND_FAILED", "duration_ms", since(start))
		b.abort(req, closeReasonSendFailed)
		return outcome{}, workspacefile.ErrTransportUnavailable
	case ctx.Err() != nil:
		return outcome{}, ctx.Err()
	default:
		req.log.Error("FILE_OPEN을 만들지 못함", "error_code", "INTERNAL_ERROR", "duration_ms", since(start))
		return outcome{}, fmt.Errorf("filetransport: FILE_OPEN 전송: %w", err)
	}

	attachTimer := time.NewTimer(b.attachTimeout)
	defer attachTimer.Stop()
	select {
	case out := <-req.result:
		// attach 전에 끝났다(FILE_OPEN_RESULT FAILED, revoke, shutdown).
		return b.complete(req, out, start)
	case <-req.attached:
	case <-attachTimer.C:
		req.log.Warn("File 요청 실패", "error_code", "ATTACH_TIMEOUT", "duration_ms", since(start))
		b.abort(req, closeReasonTimeout)
		return outcome{}, workspacefile.ErrTransportUnavailable
	case <-ctx.Done():
		req.log.Info("File 요청 취소", "reason", "request_canceled", "duration_ms", since(start))
		b.abort(req, closeReasonCanceled)
		return outcome{}, ctx.Err()
	}

	opTimer := time.NewTimer(b.opTimeout)
	defer opTimer.Stop()
	select {
	case out := <-req.result:
		return b.complete(req, out, start)
	case <-opTimer.C:
		req.log.Warn("File 요청 실패", "error_code", "OPERATION_TIMEOUT", "duration_ms", since(start))
		b.abort(req, closeReasonTimeout)
		// Save 본문을 보냈다면 저장 여부를 알 수 없다.
		return outcome{}, req.failure(workspacefile.ErrTransportUnavailable).err
	case <-ctx.Done():
		req.log.Info("File 요청 취소", "reason", "request_canceled", "duration_ms", since(start))
		b.abort(req, closeReasonCanceled)
		return outcome{}, ctx.Err()
	}
}

// complete는 끝난 요청의 결과를 기록하고 반환한다.
func (b *Broker) complete(req *request, out outcome, start time.Time) (outcome, error) {
	if out.err != nil {
		req.log.Warn("File 요청 실패", "error_code", errorCode(out.err), "duration_ms", since(start))
		if b.isClosed() {
			// 서비스가 종료되면서 끝난 요청이다. Control connection이 아직 열려 있으므로 Connector에 정리를 알린다.
			b.sendClose(req, closeReasonRestarting)
		}
		return outcome{}, out.err
	}
	req.log.Debug("File 요청 완료", "duration_ms", since(start))
	return out, nil
}

// abort는 더 이상 기다리지 않는 요청을 정리한다. Data WSS를 닫고 Connector에 FILE_CLOSE를 요청한다. 요청 context가 취소되어도
// 정리는 끝까지 수행한다. FILE_CLOSE를 보내지 못해도(Connector 단절 등) 오류가 아니다. Connector는 Data WSS 종료로도 정리한다.
func (b *Broker) abort(req *request, reason string) {
	req.shutdown(workspacefile.ErrTransportUnavailable, 1001, "request ended")
	b.sendClose(req, reason)
}

// sendClose는 Connector에 FILE_CLOSE를 요청한다. 요청 context와 무관하게 짧은 시간 안에 끝낸다.
func (b *Broker) sendClose(req *request, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	_, err := b.control.SendFileClose(ctx, connector.FileClose{
		ConnectorID: req.connectorID,
		RequestID:   req.httpRequestID,
		Correlation: connector.FileCorrelation{FileRequestID: req.id, LabInstanceID: req.labInstanceID, Generation: req.generation},
		Reason:      reason,
	})
	if err != nil {
		req.log.Debug("FILE_CLOSE 전송 안 함", "reason", closeSendFailureReason(err))
	}
}

func closeSendFailureReason(err error) string {
	switch {
	case errors.Is(err, connector.ErrCapabilityUnsupported):
		return "capability_unsupported"
	case errors.Is(err, connector.ErrConnectorUnavailable):
		return "connector_unavailable"
	default:
		return "send_failed"
	}
}

// register는 요청을 pending에 등록한다. Broker가 종료 중이면 false다.
func (b *Broker) register(req *request) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return false
	}
	b.pending[req.id] = req
	return true
}

// release는 요청의 pending 상태를 모두 지운다. Router의 FILE_OPEN pending과 bind된 Data WSS도 정리한다. 여러 번 호출해도 안전하다.
func (b *Broker) release(req *request) {
	b.mu.Lock()
	delete(b.pending, req.id)
	b.mu.Unlock()
	b.control.ForgetFileOpen(req.connectorID, req.id)

	// 성공해서 Data WSS가 이미 정상 종료되었어도 안전하다. 실패 경로에서 남은 connection이 있으면 닫는다.
	req.mu.Lock()
	conn := req.conn
	req.mu.Unlock()
	if conn != nil {
		conn.closeNow(1000, "done")
	}
}

func (b *Broker) isClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

func (b *Broker) lookup(id string) *request {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pending[id]
}

// PendingCount는 진행 중인 요청 수다(test용).
func (b *Broker) PendingCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}

// HandleFileEvent는 connector.FileSink의 구현이다. Connector가 FILE_OPEN_RESULT=FAILED로 알리면 attach를 기다리던 요청을 즉시
// 실패시킨다. SUCCEEDED는 통지일 뿐이며 실제 Data WSS attach만 근거로 삼는다. Router가 이미 인증된 Connector, fileRequestId,
// labInstanceId, generation, replyToMessageId가 모두 맞는 pending에만 연결했지만 한 번 더 확인한다.
func (b *Broker) HandleFileEvent(e connector.FileEvent) {
	switch event := e.(type) {
	case connector.FileOpenResultEvent:
		if event.Payload.Outcome != connector.FileOutcomeFailed {
			return
		}
		req := b.lookup(event.Correlation.FileRequestID)
		if req == nil || req.connectorID != event.ConnectorID ||
			req.labInstanceID != event.Correlation.LabInstanceID || req.generation != event.Correlation.Generation {
			return
		}
		// attach 전이면 요청을 실패시킨다. 이미 attach된 요청은 Data WSS가 결과를 정한다. 오류 문구는 쓰지 않고 code만 분류한다.
		code := ""
		if event.Payload.Error != nil {
			code = event.Payload.Error.Code
		}
		req.mu.Lock()
		waiting := req.state == stateWaiting
		req.mu.Unlock()
		if waiting {
			req.finish(outcome{err: mapConnectorError(code, req.op)})
		}
	case connector.FileUnmatchedEvent:
		// Router가 이미 기록했다. 어떤 요청도 완료시키지 않는다.
	}
}

// CredentialRevoked는 connector.RevokeObserver의 구현이다. 그 Credential로 인증된 File Data WSS를 close 4001로 끝내고 진행 중이던
// 요청을 실패시킨다.
func (b *Broker) CredentialRevoked(credentialID uuid.UUID) {
	for _, d := range b.trust.revokeCredential(credentialID) {
		b.revokeConn(d)
	}
}

// ConnectorRevoked는 connector.RevokeObserver의 구현이다. 그 Connector의 모든 File Data WSS를 close 4001로 끝낸다.
func (b *Broker) ConnectorRevoked(connectorID uuid.UUID) {
	for _, d := range b.trust.revokeConnector(connectorID) {
		b.revokeConn(d)
	}
}

func (b *Broker) revokeConn(d *dataConn) {
	d.closeNow(closeCredentialRevoked, "credential revoked")
	if req := d.req.Load(); req != nil {
		req.finish(req.failure(workspacefile.ErrTransportUnavailable))
	}
}

// Close는 새 요청과 새 Upgrade를 거절하고 진행 중인 요청을 모두 끝낸다(Data WSS는 1001로 닫는다). 기다리지 않는다.
// 여러 번 호출해도 안전하다. http.Server.Shutdown은 hijack된 WebSocket을 기다리거나 닫지 않으므로 호출자가 Shutdown 시 함께 호출한다.
func (b *Broker) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	close(b.done)
	pending := make([]*request, 0, len(b.pending))
	for _, req := range b.pending {
		pending = append(pending, req)
	}
	b.mu.Unlock()

	for _, req := range pending {
		req.shutdown(workspacefile.ErrTransportUnavailable, 1001, "server shutting down")
	}
}

// Shutdown은 Close를 호출하고, 열린 Data WSS와 진행 중인 요청이 모두 끝나거나 ctx가 끝날 때까지 기다린다.
func (b *Broker) Shutdown(ctx context.Context) error {
	b.Close()
	drained := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(drained)
	}()
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// enter는 Data WSS Upgrade 요청 하나의 처리를 시작한다. Close 이후에는 false다. true이면 호출자가 b.wg.Done을 호출해야 한다.
func (b *Broker) enter() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return false
	}
	b.wg.Add(1)
	return true
}

func since(start time.Time) int64 { return time.Since(start).Milliseconds() }

// mapConnectorError는 Connector가 FAILED 결과에 실은 error.code를 application error로 바꾼다. 알 수 없는 code는 사용 불가로 취급한다.
// 오류 문구(message)는 사용하지 않는다.
func mapConnectorError(code string, op operation) error {
	switch code {
	case codeNotFound:
		return workspacefile.ErrPathNotFound
	case codeNotAFile:
		return workspacefile.ErrNotAFile
	case codeNotADirectory:
		return workspacefile.ErrNotADirectory
	case codeTooLarge:
		return workspacefile.ErrTooLarge
	case codeRevisionConflict:
		if op == opSave {
			return workspacefile.ErrStaleRevision
		}
		return workspacefile.ErrTransportUnavailable
	case codePermissionDenied:
		return workspacefile.ErrFilePermissionDenied
	case codeInvalidPath:
		// SaaS가 canonical로 검증한 경로를 Connector가 거절했다(root 밖을 가리키는 등). 경로를 사용할 수 없는 것이다.
		return &workspacefile.PathError{Reason: "rejected_by_connector"}
	default:
		return workspacefile.ErrTransportUnavailable
	}
}

// errorCode는 요청 실패를 log에 남길 수 있는 고정 분류로 바꾼다.
func errorCode(err error) string {
	switch {
	case errors.Is(err, workspacefile.ErrPathNotFound):
		return "NOT_FOUND"
	case errors.Is(err, workspacefile.ErrNotAFile):
		return "NOT_A_FILE"
	case errors.Is(err, workspacefile.ErrNotADirectory):
		return "NOT_A_DIRECTORY"
	case errors.Is(err, workspacefile.ErrTooLarge):
		return "TOO_LARGE"
	case errors.Is(err, workspacefile.ErrStaleRevision):
		return "REVISION_CONFLICT"
	case errors.Is(err, workspacefile.ErrFilePermissionDenied):
		return "PERMISSION_DENIED"
	case errors.Is(err, workspacefile.ErrInvalidPath):
		return "INVALID_PATH"
	case errors.Is(err, workspacefile.ErrSaveOutcomeUnknown):
		return "SAVE_OUTCOME_UNKNOWN"
	default:
		return "UNAVAILABLE"
	}
}
