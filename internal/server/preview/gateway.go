package preview

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/observability"
)

// Preview Origin이 직접 만드는 오류 응답의 code다(openapi.yaml PreviewGatewayProblem). Workspace application의 응답은 이 code로 바꾸지 않는다.
const (
	codeUnauthenticated    = "preview_unauthenticated"
	codeTunnelClosed       = "preview_tunnel_closed"
	codeUpstreamError      = "preview_upstream_error"
	codeUpstreamTimeout    = "preview_upstream_timeout"
	codeUpgradeUnsupported = "preview_upgrade_unsupported"
	codeUnavailable        = "preview_unavailable"
	codeMethodNotAllowed   = "method_not_allowed"
	codeNotFound           = "not_found"
	codeInvalidRequest     = "invalid_request"
	codeCSRFRejected       = "csrf_rejected"
)

// upstreamHost는 Transport가 연결 pool을 구분하는 이름이다. 실제 연결은 PreviewSession의 tunnel이며 이 host를 resolve하지 않는다.
const upstreamHost = "workspace.preview.internal"

// maxExchangeBodyBytes는 bootstrap credential 교환 요청 body 상한이다. 본문은 credential 하나뿐이다.
const maxExchangeBodyBytes = 4 << 10

// Handler는 Preview Origin의 http.Handler다. request Host가 Origin template에 일치하는 요청만 다뤄야 하며(MatchesHost) 호출자가 SaaS 본 서비스
// 요청과 가른다. 예약 경로(/__labbit)를 제외한 모든 요청을 Preview Cookie로 인증한 뒤 PreviewSession의 Workspace application으로 전달한다.
func (g *Gateway) Handler() http.Handler { return http.HandlerFunc(g.serveHTTP) }

// MatchesHost는 request Host가 Preview Origin template에 일치하는지다. 일치하는 요청은 이 Gateway로, 그 밖의 요청은 SaaS 본 서비스로 간다.
func (g *Gateway) MatchesHost(host string) bool { return g.origin.MatchesHost(host) }

func (g *Gateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	// 별도 요청 ID 체계를 만들지 않고 기존 observability의 request ID를 쓴다. 클라이언트가 보낸 값은 신뢰하지 않는다.
	r = r.WithContext(observability.ContextWithRequestID(r.Context(), uuid.NewString()))
	rec := &statusRecorder{ResponseWriter: w}
	id, _ := g.origin.SessionID(r.Host)
	defer func() {
		// 경로, query, Cookie, header, 본문은 기록하지 않는다.
		g.logger.Debug("Preview 요청",
			"request_id", observability.RequestIDFromContext(r.Context()), "preview_session_id", id,
			"status", rec.status, "duration_ms", time.Since(start).Milliseconds())
	}()
	g.route(rec, r, id)
}

func (g *Gateway) route(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" {
		g.problem(w, r, http.StatusNotFound, codeNotFound, "요청한 리소스를 찾을 수 없습니다.")
		return
	}
	if !g.enter() {
		g.problem(w, r, http.StatusServiceUnavailable, codeUnavailable, "Preview를 지금 사용할 수 없습니다.")
		return
	}
	defer g.wg.Done()

	if path := r.URL.Path; path == ReservedPrefix || strings.HasPrefix(path, ReservedPrefix+"/") {
		switch path {
		case BootstrapPath:
			g.serveBootstrap(w, r, id)
		case ExchangePath:
			g.serveExchange(w, r, id)
		default:
			g.problem(w, r, http.StatusNotFound, codeNotFound, "요청한 리소스를 찾을 수 없습니다.")
		}
		return
	}

	// 인증은 PreviewSession의 상태와 Preview Cookie digest로만 한다. 알 수 없거나 종료·만료된 PreviewSession, Cookie 없음/불일치를 구분하지 않는다.
	s := g.lookup(id)
	token, presented := g.presentedCookie(r)
	if s == nil || !presented {
		g.unauthenticated(w, r)
		return
	}
	proxy, ok := s.authenticate(g.clock.Now(), token)
	if !ok || proxy == nil {
		g.unauthenticated(w, r)
		return
	}

	switch r.Method {
	case http.MethodConnect, http.MethodTrace:
		w.Header().Set("Allow", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
		g.problem(w, r, http.StatusMethodNotAllowed, codeMethodNotAllowed, "이 method는 Preview로 전달하지 않습니다.")
		return
	}
	if isUpgrade(r) {
		// MVP 범위가 아니다. PreviewSession의 tunnel은 TCP 연결 하나이므로 Upgrade된 연결이 tunnel을 독점하지 않게 거절한다.
		g.problem(w, r, http.StatusNotImplemented, codeUpgradeUnsupported, "Preview는 HTTP Upgrade(WebSocket 등)를 지원하지 않습니다.")
		return
	}
	proxy.ServeHTTP(&tolerantWriter{ResponseWriter: w}, r)
}

func isUpgrade(r *http.Request) bool {
	if r.Header.Get("Upgrade") != "" {
		return true
	}
	for _, v := range r.Header.Values("Connection") {
		for _, token := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
				return true
			}
		}
	}
	return false
}

// newTransport는 PreviewSession의 tunnel 위에서 Workspace application으로 HTTP/1.1 요청을 보내는 Transport다.
// tunnel이 TCP 연결 하나이므로 연결을 하나만 쓰고(동시 요청은 순서대로 기다린다) 새 연결을 만들지 않는다. 응답 byte를 바꾸지 않도록
// 압축을 투명하게 풀지 않는다.
func (g *Gateway) newTransport(s *session) *http.Transport {
	return &http.Transport{
		DialContext:           s.dial,
		MaxConnsPerHost:       1,
		MaxIdleConnsPerHost:   1,
		DisableCompression:    true,
		ForceAttemptHTTP2:     false,
		ResponseHeaderTimeout: g.upstreamTimeout,
	}
}

func (g *Gateway) newProxy(s *session) http.Handler {
	return &httputil.ReverseProxy{
		Rewrite:   g.rewrite,
		Transport: s.transport,
		ModifyResponse: func(res *http.Response) error {
			sanitizeSetCookies(res.Header)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) { g.proxyError(s, w, r, err) },
		// ReverseProxy의 기본 로거는 오류 문자열(URL 등)을 표준 로거로 쓴다. 경로와 query를 남기지 않도록 버린다.
		ErrorLog: log.New(io.Discard, "", 0),
	}
}

// rewrite는 Workspace application으로 보낼 요청을 만든다. Labbit 소유 credential(Preview Cookie, 로그인 Session Cookie)은 제거하고
// application이 설정한 Cookie와 application의 Authorization은 그대로 둔다(Browser의 SaaS 인증은 Cookie뿐이라 Preview Origin의
// Authorization header는 application의 것이다). Host는 Preview Origin의 host를 유지해 application이 올바른 절대 URL을 만들 수 있게 한다.
// hop-by-hop header와 client가 보낸 Forwarded/X-Forwarded-*는 ReverseProxy가 Rewrite 전에 이미 지웠다.
func (g *Gateway) rewrite(pr *httputil.ProxyRequest) {
	out := pr.Out
	out.URL.Scheme = "http"
	out.URL.Host = upstreamHost
	out.Host = pr.In.Host
	stripLabbitCookies(out.Header)
	out.Header.Set("X-Forwarded-Host", pr.In.Host)
	out.Header.Set("X-Forwarded-Proto", g.origin.Scheme())
	// Browser가 요청을 중단해도 tunnel(TCP 연결 하나)을 끊지 않도록 요청의 취소를 Workspace 쪽으로 전달하지 않는다.
	// 응답은 tolerantWriter가 끝까지 읽어 연결을 재사용 가능한 상태로 둔다.
	pr.Out = out.WithContext(context.WithoutCancel(pr.In.Context()))
}

// proxyError는 Workspace application으로의 요청이 실패했을 때의 응답이다. 오류 원문은 응답과 log에 쓰지 않는다.
func (g *Gateway) proxyError(s *session, w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errTunnelClosed), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF),
		errors.Is(err, io.ErrClosedPipe), errors.Is(err, net.ErrClosed):
		s.log.Warn("Preview 요청 실패", "request_id", observability.RequestIDFromContext(r.Context()), "error_code", "TUNNEL_CLOSED")
		g.problem(w, r, http.StatusBadGateway, codeTunnelClosed, "Preview 연결이 닫혔습니다. 새 Preview를 만들어 주세요.")
	case isTimeout(err):
		s.log.Warn("Preview 요청 실패", "request_id", observability.RequestIDFromContext(r.Context()), "error_code", "UPSTREAM_TIMEOUT")
		g.problem(w, r, http.StatusGatewayTimeout, codeUpstreamTimeout, "Workspace application이 시간 안에 응답하지 않았습니다.")
	default:
		s.log.Warn("Preview 요청 실패", "request_id", observability.RequestIDFromContext(r.Context()), "error_code", "UPSTREAM_ERROR")
		g.problem(w, r, http.StatusBadGateway, codeUpstreamError, "Workspace application의 응답을 처리하지 못했습니다.")
	}
}

// problemDetails는 Gateway가 만드는 오류 응답이다(openapi.yaml PreviewGatewayProblem). detail은 고정된 안전한 문구만 쓴다.
type problemDetails struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail,omitempty"`
	Code      string `json:"code"`
	RequestID string `json:"requestId"`
}

func (g *Gateway) problem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	body, err := json.Marshal(problemDetails{
		Type: "about:blank", Title: http.StatusText(status), Status: status, Detail: detail, Code: code,
		RequestID: observability.RequestIDFromContext(r.Context()),
	})
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (g *Gateway) unauthenticated(w http.ResponseWriter, r *http.Request) {
	g.problem(w, r, http.StatusUnauthorized, codeUnauthenticated, "Preview 인증이 필요하거나 PreviewSession이 종료되었습니다.")
}

// statusRecorder는 응답 status를 log에 남기기 위해 기록한다. Flush와 Hijack은 Unwrap으로 http.ResponseController가 찾는다.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// tolerantWriter는 Browser가 연결을 끊은 뒤의 쓰기 오류를 삼킨다. ReverseProxy가 응답 body를 끝까지 읽어 Workspace tunnel(TCP 연결 하나)이
// 계속 쓸 수 있는 상태로 남게 한다. 그렇지 않으면 Browser가 요청 하나를 중단할 때마다 PreviewSession이 끝난다.
type tolerantWriter struct {
	http.ResponseWriter
	gone bool
}

func (w *tolerantWriter) Write(p []byte) (int, error) {
	if w.gone {
		return len(p), nil
	}
	n, err := w.ResponseWriter.Write(p)
	if err != nil {
		w.gone = true
		return len(p), nil
	}
	return n, nil
}

func (w *tolerantWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Terminate는 id의 PreviewSession을 끝낸다. 처음 끝낸 호출만 true다. end.NotifyConnector이면 Lifecycle이 Connector에 PREVIEW_CLOSE를 보낸다.
// Preview Cookie는 즉시 무효가 되고 tunnel이 닫힌다.
func (g *Gateway) Terminate(id string, end End) bool {
	s := g.lookup(id)
	if s == nil {
		return false
	}
	return g.endSession(s, end, closeNormal, "session ended", false)
}

// Forget은 아직 호출자에게 성공으로 돌려주지 않은(활성화하지 않은) PreviewSession을 흔적 없이 정리한다. Lifecycle에 알리지 않고 tombstone도 남기지 않는다.
// 이미 끝났거나 없으면 아무것도 하지 않는다.
func (g *Gateway) Forget(id string) {
	s := g.lookup(id)
	if s == nil {
		return
	}
	g.endSession(s, End{Reason: "OPEN_FAILED"}, closeNormal, "open failed", true)
	g.removeSession(s)
}

// Close는 새 PreviewSession, Data WSS, Preview 요청을 거절하고 모든 PreviewSession을 SERVICE_RESTARTING으로 끝낸다. Lifecycle이 Connector에
// PREVIEW_CLOSE를 보낼 수 있도록 Connector Control connection이 열려 있는 동안 호출한다. 기다리지 않는다. 여러 번 호출해도 안전하다.
// http.Server.Shutdown은 hijack된 WebSocket을 기다리거나 닫지 않으므로 호출자가 Shutdown 시 함께 호출한다.
func (g *Gateway) Close() {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	g.closed = true
	close(g.done)
	all := make([]*session, 0, len(g.sessions))
	for _, s := range g.sessions {
		all = append(all, s)
	}
	g.mu.Unlock()

	for _, s := range all {
		g.endSession(s, End{Reason: EndServiceRestarting, NotifyConnector: true}, closeGoingAway, "server shutting down", false)
	}
}

// Shutdown은 Close를 호출하고, 열린 Data WSS와 진행 중인 Preview 요청이 모두 끝나거나 ctx가 끝날 때까지 기다린다.
func (g *Gateway) Shutdown(ctx context.Context) error {
	g.Close()
	drained := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(drained)
	}()
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func requestID(r *http.Request) string { return observability.RequestIDFromContext(r.Context()) }
