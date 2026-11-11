package valkeyowner

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// HeaderInternalPeer는 내부 Pod 간 peer proxy 요청임을 나타낸다.
	HeaderInternalPeer = "X-Labbit-Internal-Peer"
	// HeaderPeerSecret은 peer-to-peer 인증용 공유 비밀값이다.
	HeaderPeerSecret = "X-Labbit-Peer-Secret"

	// maxProxyFrameBytes는 프록시 터널에서 허용하는 최대 WebSocket 프레임 크기(16 MiB)다.
	// 초기 control 핸드셰이크의 낮은 ReadLimit(4 KiB)이 프록시 후 PTY/파일 바이너리 스트림을 차단하지 않도록 상향한다.
	maxProxyFrameBytes = 16 << 20
)

var (
	// ErrInvalidTargetAddr는 peer 대상 주소가 유효하지 않거나 SSRF 위험 대역임을 나타낸다.
	ErrInvalidTargetAddr = errors.New("valkeyowner: 유효하지 않은 peer 대상 주소")
	// ErrPeerAuthRequired는 peer secret 미설정 또는 다중 홉 루프 시도를 나타낸다.
	ErrPeerAuthRequired = errors.New("valkeyowner: peer 인증 설정 필요 또는 프록시 루프 차단")
)

var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailers":            true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

// ValidateTargetAddr는 peer 대상 주소(host:port)의 형식과 SSRF 안전성을 검증한다.
// 메타데이터 IP(169.254.0.0/16, fd00:ec2::254), 미지정 주소(0.0.0.0, ::), 멀티캐스트,
// 내부 관리 포트(9090), URL 제어문자 및 자기 자신(selfAddr)으로의 프록시를 거부한다.
func ValidateTargetAddr(targetAddr string, selfAddr string) error {
	targetAddr = strings.TrimSpace(targetAddr)
	if targetAddr == "" {
		return ErrInvalidTargetAddr
	}
	if selfAddr != "" && targetAddr == strings.TrimSpace(selfAddr) {
		return ErrInvalidTargetAddr
	}
	if strings.ContainsAny(targetAddr, "/\\@?# \t\r\n") {
		return ErrInvalidTargetAddr
	}
	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil || host == "" || portStr == "" {
		return ErrInvalidTargetAddr
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 || port == 9090 {
		return ErrInvalidTargetAddr
	}

	if ip := net.ParseIP(host); ip != nil {
		if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return ErrInvalidTargetAddr
		}
		// AWS IPv6 IMDS (fd00:ec2::254) 차단
		if strings.EqualFold(ip.String(), "fd00:ec2::254") {
			return ErrInvalidTargetAddr
		}
		return nil
	}

	lowerHost := strings.ToLower(host)
	if lowerHost == "metadata.google.internal" || lowerHost == "metadata" || lowerHost == "instance-data" {
		return ErrInvalidTargetAddr
	}
	for i := 0; i < len(host); i++ {
		c := host[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '.') {
			return ErrInvalidTargetAddr
		}
	}
	return nil
}

// Proxy는 Pod 간 HTTP 및 WebSocket 터널링 프록시다.
type Proxy struct {
	secret   string
	selfAddr string
	client   *http.Client
}

// NewProxy는 Proxy를 생성한다.
func NewProxy(secret string) *Proxy {
	return NewProxyWithSelf(secret, "")
}

// NewProxyWithSelf는 자기 자신의 PodAddr를 알고 있는 Proxy를 생성한다.
func NewProxyWithSelf(secret, selfAddr string) *Proxy {
	return &Proxy{
		secret:   secret,
		selfAddr: strings.TrimSpace(selfAddr),
		client: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// IsInternalPeer는 요청이 신뢰할 수 있는 피어로부터 왔는지 상수 시간 비교로 검사한다.
func (p *Proxy) IsInternalPeer(req *http.Request) bool {
	if p == nil || req == nil {
		return false
	}
	return p.IsInternalPeerFromHeader(req.Header)
}

// IsInternalPeerFromHeader는 헤더가 신뢰할 수 있는 피어로부터 왔는지 상수 시간 비교로 검사한다.
func (p *Proxy) IsInternalPeerFromHeader(h http.Header) bool {
	if p == nil || p.secret == "" || h == nil {
		return false
	}
	if h.Get(HeaderInternalPeer) != "true" {
		return false
	}
	got := h.Get(HeaderPeerSecret)
	if len(got) != len(p.secret) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(p.secret)) == 1
}

// HasSpoofedPeerHeader는 외부 클라이언트가 유효하지 않은 내부 피어 헤더를 위조해 보냈는지 검사한다.
func (p *Proxy) HasSpoofedPeerHeader(req *http.Request) bool {
	if req == nil {
		return false
	}
	return p.HasSpoofedPeerHeaderFromHeader(req.Header)
}

// HasSpoofedPeerHeaderFromHeader는 헤더에 위조된 내부 피어 헤더가 있는지 검사한다.
func (p *Proxy) HasSpoofedPeerHeaderFromHeader(h http.Header) bool {
	if h == nil {
		return false
	}
	hasPeerHeader := h.Get(HeaderInternalPeer) != "" || h.Get(HeaderPeerSecret) != ""
	if !hasPeerHeader {
		return false
	}
	return !p.IsInternalPeerFromHeader(h)
}

// TryProxyHTTP는 들어온 HTTP 요청을 대상 Pod로 투명하게 중계한다.
// 대상 주소 검증이나 연결/전송 단계에서 실패하면 w에 아무것도 쓰지 않고 에러를 반환하므로,
// 호출자가 자체 Problem Details 형식으로 응답할 수 있다.
func (p *Proxy) TryProxyHTTP(w http.ResponseWriter, req *http.Request, targetAddr string) error {
	if p == nil || p.secret == "" || p.IsInternalPeer(req) || p.HasSpoofedPeerHeader(req) {
		return ErrPeerAuthRequired
	}
	if err := ValidateTargetAddr(targetAddr, p.selfAddr); err != nil {
		return err
	}

	targetURL := url.URL{
		Scheme:   "http",
		Host:     targetAddr,
		Path:     req.URL.Path,
		RawQuery: req.URL.RawQuery,
	}

	proxyReq, err := http.NewRequestWithContext(req.Context(), req.Method, targetURL.String(), req.Body)
	if err != nil {
		return err
	}
	if req.Host != "" {
		proxyReq.Host = req.Host
	}

	// 안전한 헤더만 복사 (hop-by-hop 및 내부 헤더 제거)
	for k, vv := range req.Header {
		canonical := http.CanonicalHeaderKey(k)
		if hopByHopHeaders[canonical] || strings.HasPrefix(strings.ToLower(canonical), "x-labbit-") {
			continue
		}
		for _, v := range vv {
			proxyReq.Header.Add(canonical, v)
		}
	}
	// 피어 인증 헤더 추가
	proxyReq.Header.Set(HeaderInternalPeer, "true")
	proxyReq.Header.Set(HeaderPeerSecret, p.secret)

	resp, err := p.client.Do(proxyReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 응답 복사 (hop-by-hop 제외)
	for k, vv := range resp.Header {
		canonical := http.CanonicalHeaderKey(k)
		if hopByHopHeaders[canonical] {
			continue
		}
		for _, v := range vv {
			w.Header().Add(canonical, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	return nil
}

// ProxyHTTP는 들어온 HTTP 요청을 대상 Pod로 투명하게 중계한다.
func (p *Proxy) ProxyHTTP(w http.ResponseWriter, req *http.Request, targetAddr string) {
	if err := p.TryProxyHTTP(w, req, targetAddr); err != nil {
		if errors.Is(err, ErrPeerAuthRequired) {
			http.Error(w, "peer proxy unavailable", http.StatusServiceUnavailable)
			return
		}
		if errors.Is(err, ErrInvalidTargetAddr) {
			http.Error(w, "invalid peer target", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "peer communication error", http.StatusBadGateway)
	}
}

// ProxyWebSocket은 클라이언트 WebSocket 연결을 업그레이드하고 대상 Pod로 양방향 프록시한다.
// firstFrame이 제공되면 대상 Pod에 연결 후 즉시 전송한다 (attach correlation 전달용).
func (p *Proxy) ProxyWebSocket(w http.ResponseWriter, req *http.Request, targetAddr string, firstFrame []byte, subprotocol string) error {
	if p == nil || p.secret == "" || p.IsInternalPeer(req) || p.HasSpoofedPeerHeader(req) {
		http.Error(w, "peer proxy unavailable", http.StatusServiceUnavailable)
		return ErrPeerAuthRequired
	}
	if err := ValidateTargetAddr(targetAddr, p.selfAddr); err != nil {
		http.Error(w, "invalid peer target", http.StatusServiceUnavailable)
		return err
	}

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

	extra := http.Header{}
	for _, cookie := range req.Cookies() {
		extra.Add("Cookie", cookie.String())
	}
	if auth := req.Header.Get("Authorization"); auth != "" {
		extra.Set("Authorization", auth)
	}
	if origin := req.Header.Get("Origin"); origin != "" {
		extra.Set("Origin", origin)
	}
	return p.ProxyWebSocketUpstream(clientConn, targetAddr, req.URL.RequestURI(), firstFrame, subprotocol, extra)
}

// ProxyWebSocketUpstream은 이미 핸드셰이크된 clientConn을 대상 targetAddr의 path로 WebSocket 다이얼하여 연결하고 양방향으로 릴레이한다.
func (p *Proxy) ProxyWebSocketUpstream(clientConn *websocket.Conn, targetAddr string, path string, initialMsg []byte, subprotocol string, extraHeaders http.Header) error {
	if p == nil || p.secret == "" || p.IsInternalPeerFromHeader(extraHeaders) || p.HasSpoofedPeerHeaderFromHeader(extraHeaders) {
		_ = clientConn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "peer proxy unavailable"), time.Now().Add(time.Second))
		return ErrPeerAuthRequired
	}
	if err := ValidateTargetAddr(targetAddr, p.selfAddr); err != nil {
		_ = clientConn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "invalid peer target"), time.Now().Add(time.Second))
		return err
	}
	if path == "" || path[0] != '/' {
		_ = clientConn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "invalid peer path"), time.Now().Add(time.Second))
		return ErrInvalidTargetAddr
	}

	targetURL := "ws://" + targetAddr + path
	headers := http.Header{}
	for k, vv := range extraHeaders {
		canonical := http.CanonicalHeaderKey(k)
		if hopByHopHeaders[canonical] || strings.HasPrefix(strings.ToLower(canonical), "x-labbit-") {
			continue
		}
		for _, v := range vv {
			headers.Add(canonical, v)
		}
	}
	headers.Set(HeaderInternalPeer, "true")
	headers.Set(HeaderPeerSecret, p.secret)

	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}
	if subprotocol != "" {
		dialer.Subprotocols = []string{subprotocol}
	}
	upstreamConn, _, err := dialer.Dial(targetURL, headers)
	if err != nil {
		_ = clientConn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(4003, "peer connection failed"), time.Now().Add(time.Second))
		return err
	}
	defer upstreamConn.Close()

	clientConn.SetReadLimit(maxProxyFrameBytes)
	upstreamConn.SetReadLimit(maxProxyFrameBytes)

	if len(initialMsg) > 0 {
		if err := upstreamConn.WriteMessage(websocket.TextMessage, initialMsg); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var closeOnce sync.Once
	forwardClose := func(dst *websocket.Conn, err error) {
		closeOnce.Do(func() {
			code := websocket.CloseNormalClosure
			text := ""
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) {
				code = closeErr.Code
				text = closeErr.Text
			}
			if code == websocket.CloseNoStatusReceived || code == websocket.CloseAbnormalClosure || code == websocket.CloseTLSHandshake {
				code = websocket.CloseNormalClosure
				text = ""
			}
			_ = dst.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(code, text), time.Now().Add(time.Second))
		})
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// client -> upstream
	go func() {
		defer wg.Done()
		defer cancel()
		for {
			msgType, data, err := clientConn.ReadMessage()
			if err != nil {
				forwardClose(upstreamConn, err)
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
				forwardClose(clientConn, err)
				return
			}
			if err := clientConn.WriteMessage(msgType, data); err != nil {
				return
			}
		}
	}()

	<-ctx.Done()
	forwardClose(clientConn, nil)
	_ = clientConn.SetReadDeadline(time.Now())
	_ = upstreamConn.SetReadDeadline(time.Now())

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
