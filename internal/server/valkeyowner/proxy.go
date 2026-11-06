package valkeyowner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// HeaderInternalPeer는 내부 Pod 간 peer proxy 요청임을 나타낸다.
	HeaderInternalPeer = "X-Labbit-Internal-Peer"
	// HeaderPeerSecret은 peer-to-peer 인증용 공유 비밀값이다.
	HeaderPeerSecret = "X-Labbit-Peer-Secret"
)

// Proxy는 Pod 간 HTTP 및 WebSocket 터널링 프록시다.
type Proxy struct {
	secret string
	client *http.Client
}

// NewProxy는 Proxy를 생성한다.
func NewProxy(secret string) *Proxy {
	return &Proxy{
		secret: secret,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// IsInternalPeer는 요청이 신뢰할 수 있는 피어로부터 왔는지 검사한다.
func (p *Proxy) IsInternalPeer(req *http.Request) bool {
	if p.secret == "" {
		return false
	}
	return req.Header.Get(HeaderInternalPeer) == "true" && req.Header.Get(HeaderPeerSecret) == p.secret
}

// ProxyHTTP는 들어온 HTTP 요청을 대상 Pod로 투명하게 중계한다.
func (p *Proxy) ProxyHTTP(w http.ResponseWriter, req *http.Request, targetAddr string) {
	targetURL := url.URL{
		Scheme:   "http",
		Host:     targetAddr,
		Path:     req.URL.Path,
		RawQuery: req.URL.RawQuery,
	}

	proxyReq, err := http.NewRequestWithContext(req.Context(), req.Method, targetURL.String(), req.Body)
	if err != nil {
		http.Error(w, "internal proxy error", http.StatusInternalServerError)
		return
	}

	// 헤더 복사
	for k, vv := range req.Header {
		for _, v := range vv {
			proxyReq.Header.Add(k, v)
		}
	}
	// 피어 인증 헤더 추가
	proxyReq.Header.Set(HeaderInternalPeer, "true")
	if p.secret != "" {
		proxyReq.Header.Set(HeaderPeerSecret, p.secret)
	}

	resp, err := p.client.Do(proxyReq)
	if err != nil {
		http.Error(w, "peer communication error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 응답 복사
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// ProxyWebSocket은 클라이언트 WebSocket 연결을 업그레이드하고 대상 Pod로 양방향 프록시한다.
// firstFrame이 제공되면 대상 Pod에 연결 후 즉시 전송한다 (attach correlation 전달용).
func (p *Proxy) ProxyWebSocket(w http.ResponseWriter, req *http.Request, targetAddr string, firstFrame []byte, subprotocol string) error {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	if subprotocol != "" {
		upgrader.Subprotocols = []string{subprotocol}
	}

	clientConn, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		return err
	}
	defer clientConn.Close()

	targetScheme := "ws"
	if req.TLS != nil {
		targetScheme = "wss"
	}
	targetWSURL := targetScheme + "://" + targetAddr + req.URL.RequestURI()

	upstreamHeaders := http.Header{}
	for _, cookie := range req.Cookies() {
		upstreamHeaders.Add("Cookie", cookie.String())
	}
	if auth := req.Header.Get("Authorization"); auth != "" {
		upstreamHeaders.Set("Authorization", auth)
	}
	if origin := req.Header.Get("Origin"); origin != "" {
		upstreamHeaders.Set("Origin", origin)
	}
	upstreamHeaders.Set(HeaderInternalPeer, "true")
	if p.secret != "" {
		upstreamHeaders.Set(HeaderPeerSecret, p.secret)
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}
	if subprotocol != "" {
		dialer.Subprotocols = []string{subprotocol}
	}

	upstreamConn, _, err := dialer.DialContext(req.Context(), targetWSURL, upstreamHeaders)
	if err != nil {
		_ = clientConn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(4003, "peer connection failed"))
		return err
	}
	defer upstreamConn.Close()

	// 이미 수신한 첫 프레임(attach frame)이 있다면 upstream으로 즉시 전달
	if len(firstFrame) > 0 {
		if err := upstreamConn.WriteMessage(websocket.TextMessage, firstFrame); err != nil {
			return err
		}
	}

	// 양방향 터널 펌프 (메모리 스트리밍, PTY 비영속 원칙 준수)
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	// client -> upstream
	go func() {
		defer wg.Done()
		defer cancel()
		for {
			msgType, data, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
			if err := upstreamConn.WriteMessage(msgType, data); err != nil {
				return
			}
		}
	}()

	// upstream -> client
	go func() {
		defer wg.Done()
		defer cancel()
		for {
			msgType, data, err := upstreamConn.ReadMessage()
			if err != nil {
				return
			}
			if err := clientConn.WriteMessage(msgType, data); err != nil {
				return
			}
		}
	}()

	<-ctx.Done()
	// graceful close signaling
	_ = clientConn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	_ = upstreamConn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))

	// timeout으로 고아 고루틴 방지
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
	}

	return nil
}

// ProxyWebSocketUpstream은 이미 핸드셰이크된 clientConn을 대상 targetAddr의 path로 WebSocket 다이얼하여 연결하고 양방향으로 릴레이한다.
func (p *Proxy) ProxyWebSocketUpstream(clientConn *websocket.Conn, targetAddr string, path string, initialMsg []byte, subprotocol string, extraHeaders http.Header) error {
	targetURL := "ws://" + targetAddr + path
	headers := http.Header{}
	for k, vv := range extraHeaders {
		for _, v := range vv {
			headers.Add(k, v)
		}
	}
	headers.Set(HeaderInternalPeer, "true")
	if p.secret != "" {
		headers.Set(HeaderPeerSecret, p.secret)
	}
	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}
	if subprotocol != "" {
		dialer.Subprotocols = []string{subprotocol}
	}
	upstreamConn, _, err := dialer.Dial(targetURL, headers)
	if err != nil {
		_ = clientConn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(4003, "peer connection failed"))
		return err
	}
	defer upstreamConn.Close()

	if len(initialMsg) > 0 {
		if err := upstreamConn.WriteMessage(websocket.TextMessage, initialMsg); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	// client -> upstream
	go func() {
		defer wg.Done()
		defer cancel()
		for {
			msgType, data, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
			if err := upstreamConn.WriteMessage(msgType, data); err != nil {
				return
			}
		}
	}()

	// upstream -> client
	go func() {
		defer wg.Done()
		defer cancel()
		for {
			msgType, data, err := upstreamConn.ReadMessage()
			if err != nil {
				return
			}
			if err := clientConn.WriteMessage(msgType, data); err != nil {
				return
			}
		}
	}()

	<-ctx.Done()
	_ = clientConn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	_ = upstreamConn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
	}
	return nil
}

// ExtractSessionIDFromAttach는 JSON attach 프레임에서 terminalSessionId를 추출한다.
func ExtractSessionIDFromAttach(payload []byte) (string, bool) {
	// 정규 JSON 파싱
	type attachPartial struct {
		Type              string `json:"type"`
		TerminalSessionID string `json:"terminalSessionId"`
	}
	var p attachPartial
	if err := json.Unmarshal(payload, &p); err == nil && p.TerminalSessionID != "" {
		return p.TerminalSessionID, true
	}
	// 대소문자나 키 변형 대응 (일부 프레임: terminal_session_id)
	type attachSnake struct {
		TerminalSessionID string `json:"terminal_session_id"`
	}
	var s attachSnake
	if err := json.Unmarshal(payload, &s); err == nil && s.TerminalSessionID != "" {
		return s.TerminalSessionID, true
	}
	return "", false
}
