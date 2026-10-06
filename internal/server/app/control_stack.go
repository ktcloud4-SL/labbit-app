package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/observability"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/server/auth"
	"github.com/ktcloud4-SL/labbit-app/internal/server/class"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connectorwss"
	"github.com/ktcloud4-SL/labbit-app/internal/server/filetransport"
	"github.com/ktcloud4-SL/labbit-app/internal/server/httpapi"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal"
	"github.com/ktcloud4-SL/labbit-app/internal/server/workspacefile"
)

// stackOptions는 api role의 조립 입력이다. 시간 값은 0이면 각 구성 요소의 계약 기본값이며, Run은 기본값만 쓴다.
// 0이 아닌 값은 60초 grace나 heartbeat 같은 긴 시간을 test에서 기다리지 않도록 주입하는 용도다.
type stackOptions struct {
	Logger *slog.Logger
	// PublicOrigin은 httpapi.ParseOrigin으로 정규화한 trusted origin이다. HTTP unsafe method와 Browser WSS Upgrade의 Origin 검증에 쓴다.
	PublicOrigin string
	// Realtime이 true이면 Terminal Relay와 TerminalSession 생성/종료를 함께 조립한다(api와 realtime role이 같은 process).
	Realtime        bool
	HTTPMetrics     *observability.HTTPMetrics
	RealtimeMetrics *observability.RealtimeMetrics

	Clock         realtime.Clock
	Grace         time.Duration
	OpenTimeout   time.Duration
	AttachTimeout time.Duration
	CloseGrace    time.Duration

	// FileAttachTimeout과 FileOperationTimeout은 Workspace File 요청이 Connector의 File Data WSS attach와 결과를 기다리는 시간이고,
	// MaxFileBytes는 파일 크기 한도(HTTP 413)다. 0이면 각 구성 요소의 기본값이다. test에서 짧은 시간과 작은 한도를 주입하는 용도다.
	FileAttachTimeout    time.Duration
	FileOperationTimeout time.Duration
	MaxFileBytes         int64
}

// controlStack은 api role이 소유하는 handler와 Connector Control 경계, 그리고 선택적으로 같은 process의 Terminal Relay다.
// Run과 통합 test가 같은 조립을 사용한다.
type controlStack struct {
	API              http.Handler
	ConnectorControl *connectorwss.Handler
	Registry         *connector.Registry
	Router           *connector.Router

	// Terminals와 Relay는 Realtime이 false이면 nil이다.
	Terminals *terminal.Service
	Relay     *realtime.Relay

	// Files와 FileBroker는 api role이 소유하는 Workspace File use case와 Connector File transport다. realtime role이 필요하지 않다.
	Files      *workspacefile.Service
	FileBroker *filetransport.Broker
}

// newControlStack은 store 위에서 Auth, Class, Connector Control, (선택) Terminal을 조립한다.
func newControlStack(store *postgres.Store, opts stackOptions) (*controlStack, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	authService := auth.NewService(store, auth.Argon2id{}, nil)
	connectorService := connector.NewService(store)
	registry := connector.NewRegistry()

	// Router와 terminal.Service는 서로를 필요로 한다(Router가 결과를 Service에 넘기고 Service가 Router로 보낸다).
	// 조립 시점에 forwarder로 순환을 끊는다. 서비스를 시작하기 전에 target이 채워지며 이후 바뀌지 않는다.
	sink := &terminalSinkForwarder{}
	// Router와 File transport Broker도 같은 관계다. Router가 FILE_OPEN_RESULT를 Broker에 넘기고 Broker가 Router로 FILE_OPEN을 보낸다.
	fileSink := &fileSinkForwarder{}
	routerOpts := connector.RouterOptions{Registry: registry, Logger: logger, FileSink: fileSink}
	if opts.Realtime {
		routerOpts.TerminalSink = sink
	}
	// Sink(Operation 결과)는 durable Operation Worker(LBT-18)가 생기기 전이라 두지 않는다.
	router, err := connector.NewRouter(routerOpts)
	if err != nil {
		return nil, err
	}

	stack := &controlStack{Registry: registry, Router: router}

	// Workspace File은 api role의 일이다. 권한 판정(workspacefile.Service)은 PostgreSQL 상태를 쓰고, 파일 I/O는 Connector Control의
	// FILE_OPEN과 요청별 File Data WSS(filetransport.Broker)로 한다. Broker의 pending 요청은 process 안의 ephemeral 상태다.
	fileBroker, err := filetransport.New(filetransport.Options{
		Control: router, Auth: connectorService, Logger: logger,
		AttachTimeout: opts.FileAttachTimeout, OperationTimeout: opts.FileOperationTimeout,
	})
	if err != nil {
		return nil, err
	}
	fileSink.target = fileBroker
	files, err := workspacefile.NewService(workspacefile.Options{
		Store: store, Transport: fileBroker, MaxFileBytes: opts.MaxFileBytes, Logger: logger,
	})
	if err != nil {
		return nil, err
	}
	stack.Files, stack.FileBroker = files, fileBroker
	// Credential/Connector revoke를 Control이 관측하면 같은 Credential로 인증된 File Data WSS도 함께 종료한다.
	observers := revokeObservers{fileBroker}

	var terminals httpapi.Terminals
	if opts.Realtime {
		relayFwd := &relayForwarder{}
		service, err := terminal.NewService(terminal.Options{
			Store: store, Auth: authService, Connectors: router, Relay: relayFwd,
			Clock: opts.Clock, Logger: logger, OpenTimeout: opts.OpenTimeout, Grace: opts.Grace,
		})
		if err != nil {
			return nil, err
		}
		trusted := opts.PublicOrigin
		relay, err := realtime.New(realtime.Options{
			Control:       service,
			Connectors:    terminal.NewConnectorAuthenticator(connectorService),
			AllowOrigin:   func(origin string) bool { return httpapi.OriginMatches(trusted, origin) },
			Clock:         opts.Clock,
			Logger:        logger,
			Metrics:       opts.RealtimeMetrics,
			Grace:         opts.Grace,
			AttachTimeout: opts.AttachTimeout,
			CloseGrace:    opts.CloseGrace,
		})
		if err != nil {
			return nil, err
		}
		relayFwd.target = relay
		sink.target = service
		// Credential/Connector revoke를 Control이 관측하면 같은 Credential로 인증된 Terminal Data WSS도 함께 종료한다.
		observers = append(observers, terminal.NewRevokeBridge(relay))
		stack.Terminals, stack.Relay = service, relay
		terminals = service
	}

	registry.SetRevokeObserver(observers)

	stack.API, err = httpapi.New(httpapi.Options{
		Auth:         authService,
		Classes:      class.NewService(store),
		Terminals:    terminals,
		Files:        files,
		PublicOrigin: opts.PublicOrigin,
		Logger:       logger,
		Metrics:      opts.HTTPMetrics,
	})
	if err != nil {
		return nil, err
	}
	stack.ConnectorControl, err = connectorwss.New(connectorwss.Options{
		Auth:       connectorService,
		Heartbeats: connectorService,
		Registry:   registry,
		Router:     router,
		Logger:     logger,
	})
	if err != nil {
		return nil, err
	}
	return stack, nil
}

// routes는 application listener에 mount할 handler들이다.
func (c *controlStack) routes() routes {
	r := routes{API: c.API, ConnectorControl: c.ConnectorControl, ConnectorFileData: c.FileBroker.Handler()}
	if c.Relay != nil {
		r.BrowserTerminal = c.Relay.BrowserHandler()
		r.ConnectorTerminalData = c.Relay.DataHandler()
	}
	return r
}

// close는 Shutdown 시작과 함께 호출하며 새 Upgrade를 거절하게 한다. 기다리지 않는다.
// http.Server.Shutdown은 hijack된 WebSocket을 기다리거나 닫지 않으므로 handler가 새 Upgrade 거절과 기존 connection drain을 맡는다.
//
// Terminal Relay가 있으면 Connector Control connection을 여기서 닫지 않는다. Relay가 active TerminalSession을 종료하면서 그
// connection으로 TERMINAL_CLOSE를 보내야 하므로 shutdown이 마지막에 닫는다. Relay가 없는 구성(api만 enabled)은 예전처럼 바로 닫는다.
func (c *controlStack) close() {
	// 새 File 요청과 Upgrade를 거절하고 진행 중인 요청을 끝낸다. 요청마다 FILE_CLOSE를 보내려면 Control connection이 아직 열려 있어야 하므로
	// Control은 아래 기존 순서대로 닫는다.
	c.FileBroker.Close()
	if c.Relay != nil {
		c.Relay.Close()
		return
	}
	c.ConnectorControl.Close()
}

// shutdown은 열린 WebSocket과 진행 중인 작업이 끝나거나 ctx가 끝날 때까지 기다린다.
//
// 순서가 중요하다. Relay가 active TerminalSession을 SERVICE_RESTARTING으로 종료하면서 Connector에 TERMINAL_CLOSE를 보내므로
// Connector Control connection은 그 뒤에 닫는다(ConnectorControl.Shutdown이 새 Upgrade를 거절하고 열린 connection을 닫는다).
func (c *controlStack) shutdown(ctx context.Context) error {
	var errs []error
	if c.Relay != nil {
		errs = append(errs, c.Relay.Shutdown(ctx))
	}
	if c.Terminals != nil {
		errs = append(errs, c.Terminals.Shutdown(ctx))
	}
	errs = append(errs, c.FileBroker.Shutdown(ctx))
	errs = append(errs, c.ConnectorControl.Shutdown(ctx))
	return errors.Join(errs...)
}

// terminalSinkForwarder는 조립 시점에 target이 정해지는 connector.TerminalSink다.
type terminalSinkForwarder struct{ target connector.TerminalSink }

func (f *terminalSinkForwarder) HandleTerminalEvent(e connector.TerminalEvent) {
	f.target.HandleTerminalEvent(e)
}

// relayForwarder는 조립 시점에 target이 정해지는 terminal.Relay다.
type relayForwarder struct{ target terminal.Relay }

func (f *relayForwarder) Expect(e realtime.Expected) error { return f.target.Expect(e) }
func (f *relayForwarder) Activate(id string, graceExpiresAt time.Time) error {
	return f.target.Activate(id, graceExpiresAt)
}
func (f *relayForwarder) Forget(id string)                      { f.target.Forget(id) }
func (f *relayForwarder) Terminate(id string, end realtime.End) { f.target.Terminate(id, end) }

// fileSinkForwarder는 조립 시점에 target이 정해지는 connector.FileSink다.
type fileSinkForwarder struct{ target connector.FileSink }

func (f *fileSinkForwarder) HandleFileEvent(e connector.FileEvent) { f.target.HandleFileEvent(e) }

// revokeObservers는 Registry의 단일 revoke observer 자리에서 여러 Data WSS(Terminal, File)에 revoke를 전달한다.
type revokeObservers []connector.RevokeObserver

func (o revokeObservers) CredentialRevoked(credentialID uuid.UUID) {
	for _, observer := range o {
		observer.CredentialRevoked(credentialID)
	}
}

func (o revokeObservers) ConnectorRevoked(connectorID uuid.UUID) {
	for _, observer := range o {
		observer.ConnectorRevoked(connectorID)
	}
}
