package preview

import (
	"strings"
	"testing"
)

func TestParseOriginTemplate(t *testing.T) {
	valid := []struct {
		raw        string
		production bool
		want       string
		secure     bool
	}{
		{"https://{sessionId}.preview.example.com", true, "https://{sessionId}.preview.example.com", true},
		{"HTTPS://{sessionId}.Preview.Example.COM", false, "https://{sessionId}.preview.example.com", true},
		{"http://{sessionId}.localhost:8080", false, "http://{sessionId}.localhost:8080", false},
		{"https://{sessionId}.preview.example.com:443", true, "https://{sessionId}.preview.example.com", true},
		{"http://{sessionId}.preview.example.com:80", false, "http://{sessionId}.preview.example.com", false},
		{"https://{sessionId}.preview.example.com:8443", true, "https://{sessionId}.preview.example.com:8443", true},
		{"  https://{sessionId}.preview.example.com  ", true, "https://{sessionId}.preview.example.com", true},
	}
	for _, tc := range valid {
		got, err := ParseOriginTemplate(tc.raw, tc.production)
		if err != nil {
			t.Errorf("ParseOriginTemplate(%q) error = %v", tc.raw, err)
			continue
		}
		if got.String() != tc.want || got.Secure() != tc.secure {
			t.Errorf("ParseOriginTemplate(%q) = %q secure=%v, want %q secure=%v", tc.raw, got.String(), got.Secure(), tc.want, tc.secure)
		}
	}

	invalid := map[string]string{
		"비어 있음":                "",
		"placeholder 없음":       "https://preview.example.com",
		"placeholder 둘":        "https://{sessionId}.{sessionId}.example.com",
		"첫 label이 아님":          "https://app.{sessionId}.example.com",
		"label 일부":             "https://x{sessionId}.example.com",
		"path에 있음":             "https://preview.example.com/{sessionId}",
		"query에 있음":            "https://preview.example.com?x={sessionId}",
		"port에 있음":             "https://preview.example.com:{sessionId}",
		"scheme 없음":            "{sessionId}.preview.example.com",
		"scheme이 ftp":          "ftp://{sessionId}.preview.example.com",
		"path 있음":              "https://{sessionId}.preview.example.com/app",
		"trailing slash":       "https://{sessionId}.preview.example.com/",
		"query 있음":             "https://{sessionId}.preview.example.com?a=b",
		"fragment 있음":          "https://{sessionId}.preview.example.com#x",
		"userinfo 있음":          "https://user@{sessionId}.preview.example.com",
		"host가 비어 있음":          "https://{sessionId}.",
		"IP literal":           "https://{sessionId}.127.0.0.1",
		"IPv6 literal":         "https://{sessionId}.[::1]",
		"port 0":               "https://{sessionId}.preview.example.com:0",
		"port 범위 초과":           "https://{sessionId}.preview.example.com:70000",
		"port 문자":              "https://{sessionId}.preview.example.com:abc",
		"빈 label":              "https://{sessionId}..example.com",
		"대문자 placeholder":      "https://{SessionId}.preview.example.com",
		"공백이 있는 host":          "https://{sessionId}.preview example.com",
		"label이 '-'로 시작":       "https://{sessionId}.-preview.example.com",
		"placeholder 뒤에 점이 아님": "https://{sessionId}preview.example.com",
		"placeholder만 있고 나머지 host 없음": "https://{sessionId}",
	}
	for name, raw := range invalid {
		if _, err := ParseOriginTemplate(raw, false); err == nil {
			t.Errorf("%s: ParseOriginTemplate(%q)가 오류 없이 통과함", name, raw)
		}
	}

	if _, err := ParseOriginTemplate("http://{sessionId}.preview.example.com", true); err == nil {
		t.Error("production에서 http template을 받아들임")
	}
}

func TestOriginTemplateMatchesPreviewHostsOnly(t *testing.T) {
	https, _ := ParseOriginTemplate("https://{sessionId}.preview.example.com", true)
	dev, _ := ParseOriginTemplate("http://{sessionId}.localhost:8080", false)
	const id = "6f1c2f64-9a41-4d4f-8e11-0a2b3c4d5e6f"

	cases := []struct {
		name string
		tmpl OriginTemplate
		host string
		want string // 빈 문자열이면 일치하지 않음
	}{
		{"정확한 host", https, id + ".preview.example.com", id},
		{"대문자 host", https, strings.ToUpper(id) + ".PREVIEW.EXAMPLE.COM", id},
		{"기본 port가 명시된 host", https, id + ".preview.example.com:443", id},
		{"dev port 일치", dev, id + ".localhost:8080", id},
		{"dev port 다름", dev, id + ".localhost:9090", ""},
		{"dev port 없음", dev, id + ".localhost", ""},
		{"https template에 다른 port", https, id + ".preview.example.com:8443", ""},
		{"본 서비스 host", https, "labbit.example.com", ""},
		{"preview 접미사 없는 host", https, id + ".example.com", ""},
		{"label이 둘", https, "a." + id + ".preview.example.com", ""},
		{"label이 비어 있음", https, ".preview.example.com", ""},
		{"접미사만", https, "preview.example.com", ""},
		{"label에 밑줄", https, "a_b.preview.example.com", ""},
		{"label이 너무 김", https, strings.Repeat("a", 64) + ".preview.example.com", ""},
		{"suffix를 흉내 낸 다른 host", https, id + ".preview.example.com.evil.test", ""},
		{"접두 문자열만 붙은 host", https, id + "xpreview.example.com", ""},
	}
	for _, tc := range cases {
		got, ok := tc.tmpl.SessionID(tc.host)
		if tc.want == "" {
			if ok {
				t.Errorf("%s: SessionID(%q) = %q, want 일치하지 않음", tc.name, tc.host, got)
			}
			if tc.tmpl.MatchesHost(tc.host) {
				t.Errorf("%s: MatchesHost(%q) = true", tc.name, tc.host)
			}
			continue
		}
		if !ok || got != strings.ToLower(tc.want) {
			t.Errorf("%s: SessionID(%q) = %q, %v, want %q", tc.name, tc.host, got, ok, tc.want)
		}
	}

	// Origin은 Origin header와 정확히 비교할 수 있는 정규화 형태다. VM IP나 port 정보가 없다.
	if got := https.Origin(id); got != "https://"+id+".preview.example.com" {
		t.Errorf("Origin = %q", got)
	}
	if got := dev.Origin(id); got != "http://"+id+".localhost:8080" {
		t.Errorf("dev Origin = %q", got)
	}
}

// 본 서비스 Origin의 host가 Preview template에 일치하면 본 서비스 요청이 Gateway로 가버리고 사용자 코드가 본 서비스 Origin에서 실행된다.
func TestOriginTemplateConflictsWithTheMainOrigin(t *testing.T) {
	tmpl, _ := ParseOriginTemplate("https://{sessionId}.preview.example.com", true)
	if tmpl.ConflictsWith("https://labbit.example.com") {
		t.Error("서로 다른 host를 충돌로 판정함")
	}
	if tmpl.ConflictsWith("https://preview.example.com") {
		t.Error("접미사만 같은 host를 충돌로 판정함")
	}
	if !tmpl.ConflictsWith("https://app.preview.example.com") {
		t.Error("template에 일치하는 본 서비스 host를 충돌로 판정하지 않음")
	}
	dev, _ := ParseOriginTemplate("http://{sessionId}.localhost:8080", false)
	if !dev.ConflictsWith("http://app.localhost:8080") || dev.ConflictsWith("http://localhost:8080") || dev.ConflictsWith("http://app.localhost:5173") {
		t.Error("dev template의 충돌 판정이 틀림")
	}
	if tmpl.ConflictsWith("not-an-origin") {
		t.Error("origin이 아닌 값을 충돌로 판정함")
	}
}

func TestValidateSameSite(t *testing.T) {
	prodTmpl, _ := ParseOriginTemplate("https://{sessionId}.preview.example.com", true)
	devTmpl, _ := ParseOriginTemplate("http://{sessionId}.localhost:8080", false)

	valid := []struct {
		name         string
		tmpl         OriginTemplate
		publicOrigin string
	}{
		{"different origin same site prod", prodTmpl, "https://app.example.com"},
		{"different origin same site with port", prodTmpl, "https://app.example.com:8443"},
		{"same site deeper subdomain", prodTmpl, "https://sub.app.example.com"},
		{"localhost dev different ports", devTmpl, "http://localhost:5173"},
		{"localhost dev with subdomain", devTmpl, "http://app.localhost:5173"},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.tmpl.ValidateSameSite(tc.publicOrigin); err != nil {
				t.Errorf("ValidateSameSite(%q) unexpected error: %v", tc.publicOrigin, err)
			}
		})
	}

	invalid := []struct {
		name         string
		tmpl         OriginTemplate
		publicOrigin string
		wantErr      string
	}{
		{"different registrable domain (cross-site)", prodTmpl, "https://app.other-preview.net", "registrable domain이 다릅니다"},
		{"deceptive suffix (com.evil)", prodTmpl, "https://app.example.com.evil", "registrable domain이 다릅니다"},
		{"production scheme mismatch (http vs https)", prodTmpl, "http://app.example.com", "scheme 불일치"},
		{"scheme mismatch (https vs http on dev)", devTmpl, "https://localhost:5173", "scheme 불일치"},
		{"same origin conflict", prodTmpl, "https://app.preview.example.com", "다른 Origin이어야 합니다"},
		{"localhost vs non-localhost", devTmpl, "http://example.com", "불일치합니다 (한쪽만 localhost)"},
		{"non-localhost vs localhost", prodTmpl, "https://localhost", "불일치합니다 (한쪽만 localhost)"},
		{"not an origin URL", prodTmpl, "not-an-origin", "절대 origin URL이어야 합니다"},
		{"origin URL with path", prodTmpl, "https://app.example.com/api", "절대 origin URL이어야 합니다"},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.tmpl.ValidateSameSite(tc.publicOrigin)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateSameSite(%q) error = %v, want containing %q", tc.publicOrigin, err, tc.wantErr)
			}
		})
	}
}

func TestValidLabel(t *testing.T) {
	for _, ok := range []string{"a", "abc-123", "6f1c2f64-9a41-4d4f-8e11-0a2b3c4d5e6f", strings.Repeat("a", 63)} {
		if !validLabel(ok) {
			t.Errorf("validLabel(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "-a", "a-", "A", "a.b", "a_b", "a b", strings.Repeat("a", 64), "한글"} {
		if validLabel(bad) {
			t.Errorf("validLabel(%q) = true", bad)
		}
	}
}
