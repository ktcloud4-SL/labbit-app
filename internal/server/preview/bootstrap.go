package preview

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
)

// bootstrapScript는 Preview Origin의 bootstrap 페이지가 실행하는 유일한 script다. fragment의 일회용 credential을 읽어 주소에서 지우고
// 같은 Origin의 /__labbit/exchange로 교환한 뒤 Workspace application의 /로 이동한다. fragment는 HTTP request target, Referer, 서버·proxy
// access log로 전송되지 않는다. credential을 query나 path로 옮기지 않으며 어디에도 기록하지 않는다.
const bootstrapScript = `(function () {
  var status = document.getElementById('status');
  function fail(message) { status.textContent = message; }
  var credential = location.hash.slice(1);
  history.replaceState(null, '', location.pathname);
  if (!credential) { fail('Preview 링크가 없거나 이미 사용되었습니다.'); return; }
  fetch('/__labbit/exchange', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ credential: credential })
  }).then(function (response) {
    if (response.status === 204) { location.replace('/'); return; }
    fail('Preview 링크가 유효하지 않거나 만료되었습니다.');
  }).catch(function () { fail('Preview를 열지 못했습니다.'); });
})();`

// bootstrapPage는 모든 PreviewSession에 같은 정적 HTML이다. credential이나 사용자 정보를 담지 않는다.
var bootstrapPage = `<!doctype html>
<html lang="ko">
<head>
<meta charset="utf-8">
<meta name="referrer" content="no-referrer">
<title>Labbit Preview</title>
</head>
<body>
<p id="status">Preview를 여는 중입니다…</p>
<noscript>Preview를 열려면 JavaScript가 필요합니다.</noscript>
<script>` + bootstrapScript + `</script>
</body>
</html>
`

// bootstrapCSP는 inline script를 이 script의 hash로만 허용한다. 같은 Origin으로의 fetch만 허용하고 그 밖의 모든 로드를 막는다.
var bootstrapCSP = func() string {
	sum := sha256.Sum256([]byte(bootstrapScript))
	return "default-src 'none'; script-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) +
		"'; connect-src 'self'; base-uri 'none'; form-action 'none'"
}()

func (g *Gateway) serveBootstrap(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		g.problem(w, r, http.StatusMethodNotAllowed, codeMethodNotAllowed, "이 method는 허용되지 않습니다.")
		return
	}
	if s := g.lookup(id); s == nil || !s.active(g.clock.Now()) {
		g.unauthenticated(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", bootstrapCSP)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = io.WriteString(w, bootstrapPage)
	}
}

// exchangeRequest는 openapi.yaml PreviewExchangeRequest다.
type exchangeRequest struct {
	Credential string `json:"credential"`
}

func (g *Gateway) serveExchange(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		g.problem(w, r, http.StatusMethodNotAllowed, codeMethodNotAllowed, "이 method는 허용되지 않습니다.")
		return
	}
	// 같은 Preview Origin의 JavaScript만 교환할 수 있다. Origin이 없거나 이 PreviewSession의 Origin과 정확히 같지 않으면 거절한다.
	origins := r.Header.Values("Origin")
	if len(origins) != 1 || origins[0] != g.origin.Origin(id) {
		g.problem(w, r, http.StatusForbidden, codeCSRFRejected, "요청의 출처를 확인할 수 없어 거절했습니다.")
		return
	}
	req, ok := decodeExchangeRequest(w, r)
	if !ok {
		g.problem(w, r, http.StatusBadRequest, codeInvalidRequest, "요청 형식이 올바르지 않습니다.")
		return
	}

	now := g.clock.Now()
	s := g.lookup(id)
	if s == nil {
		g.unauthenticated(w, r)
		return
	}
	token, expiresAt, ok := s.exchange(now, req.Credential)
	if !ok {
		s.log.Warn("Preview bootstrap 교환 거절", "request_id", requestID(r), "error_code", "BOOTSTRAP_REJECTED")
		g.unauthenticated(w, r)
		return
	}
	http.SetCookie(w, g.newCookie(token, expiresAt, now))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
	s.log.Info("Preview bootstrap 교환", "request_id", requestID(r))
}

// decodeExchangeRequest는 application/json 하나의 {"credential": <비어 있지 않은 string>}만 받는다. 추가 field는 거절한다.
func decodeExchangeRequest(w http.ResponseWriter, r *http.Request) (exchangeRequest, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return exchangeRequest{}, false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxExchangeBodyBytes))
	decoder.DisallowUnknownFields()
	var req exchangeRequest
	if err := decoder.Decode(&req); err != nil {
		return exchangeRequest{}, false
	}
	// JSON 값 하나만 허용한다.
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return exchangeRequest{}, false
	}
	if req.Credential == "" {
		return exchangeRequest{}, false
	}
	return req, true
}
