package httpapi

import (
	"net/http"
	"testing"
)

func TestParseOrigin(t *testing.T) {
	valid := map[string]string{
		"https://labbit.example.com":      "https://labbit.example.com",
		"http://localhost:5173":           "http://localhost:5173",
		"HTTPS://Labbit.Example.COM":      "https://labbit.example.com",
		"https://labbit.example.com:443":  "https://labbit.example.com",
		"http://labbit.example.com:80":    "http://labbit.example.com",
		"https://labbit.example.com:8443": "https://labbit.example.com:8443",
		"http://[::1]:5173":               "http://[::1]:5173",
		"https://[2001:db8::1]":           "https://[2001:db8::1]",
	}
	for raw, want := range valid {
		got, err := ParseOrigin(raw)
		if err != nil || got != want {
			t.Errorf("ParseOrigin(%q) = (%q, %v), want (%q, nil)", raw, got, err, want)
		}
	}

	invalid := []string{
		"",
		"labbit.example.com",
		"//labbit.example.com",
		"null",
		"https://",
		"ftp://labbit.example.com",
		"javascript:alert(1)",
		"https://labbit.example.com/",
		"https://labbit.example.com/app",
		"https://labbit.example.com?x=1",
		"https://labbit.example.com?",
		"https://labbit.example.com#frag",
		"https://labbit.example.com#",
		"https://user@labbit.example.com",
		"https://user:pass@labbit.example.com",
		"https://labbit.example.com:0",
		"https://labbit.example.com:99999",
		"https://labbit.example.com:port",
		" https://labbit.example.com",
	}
	for _, raw := range invalid {
		if got, err := ParseOrigin(raw); err == nil {
			t.Errorf("ParseOrigin(%q) = %q, want error", raw, got)
		}
	}
}

func TestSourceAllowed(t *testing.T) {
	const trusted = "https://labbit.example.com"

	tests := []struct {
		name   string
		header http.Header
		want   bool
	}{
		// Origin이 있으면 scheme + host + port가 정확히 일치해야 한다.
		{name: "origin exact match", header: header("Origin", "https://labbit.example.com"), want: true},
		{name: "origin case and default port normalize", header: header("Origin", "HTTPS://LABBIT.example.com:443"), want: true},
		{name: "origin different scheme", header: header("Origin", "http://labbit.example.com")},
		{name: "origin different host", header: header("Origin", "https://evil.example.com")},
		{name: "origin subdomain", header: header("Origin", "https://a.labbit.example.com")},
		{name: "origin suffix host", header: header("Origin", "https://labbit.example.com.evil.test")},
		{name: "origin userinfo trick", header: header("Origin", "https://labbit.example.com@evil.test")},
		{name: "origin different port", header: header("Origin", "https://labbit.example.com:8443")},
		{name: "origin null", header: header("Origin", "null")},
		{name: "origin empty", header: header("Origin", "")},
		{name: "origin malformed", header: header("Origin", "not a url")},
		{name: "origin with path", header: header("Origin", "https://labbit.example.com/x")},
		{name: "origin non-http scheme", header: header("Origin", "chrome-extension://labbit.example.com")},
		{name: "multiple origin headers", header: http.Header{"Origin": {"https://labbit.example.com", "https://labbit.example.com"}}},

		// Origin이 없을 때만 Referer의 origin을 fallback으로 비교한다.
		{name: "referer origin match", header: header("Referer", "https://labbit.example.com/"), want: true},
		{name: "referer with path and query", header: header("Referer", "https://labbit.example.com/classes/1?tab=a"), want: true},
		{name: "referer different host", header: header("Referer", "https://evil.example.com/https://labbit.example.com/")},
		{name: "referer different scheme", header: header("Referer", "http://labbit.example.com/")},
		{name: "referer host prefix", header: header("Referer", "https://labbit.example.com.evil.test/")},
		{name: "referer malformed", header: header("Referer", "://bad")},
		{name: "referer relative", header: header("Referer", "/classes")},
		{name: "referer empty", header: header("Referer", "")},
		{name: "referer null", header: header("Referer", "null")},
		{name: "multiple referer headers", header: http.Header{"Referer": {"https://labbit.example.com/", "https://labbit.example.com/"}}},

		// Origin이 있으면 Referer로 넘어가지 않는다.
		{name: "origin wins over matching referer", header: http.Header{"Origin": {"https://evil.example.com"}, "Referer": {"https://labbit.example.com/"}}},
		{name: "matching origin wins over bad referer", header: http.Header{"Origin": {"https://labbit.example.com"}, "Referer": {"https://evil.example.com/"}}, want: true},
		{name: "null origin does not fall back to referer", header: http.Header{"Origin": {"null"}, "Referer": {"https://labbit.example.com/"}}},

		{name: "neither origin nor referer", header: http.Header{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sourceAllowed(trusted, tt.header); got != tt.want {
				t.Fatalf("sourceAllowed(%v) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}

// Host와 X-Forwarded-Host는 trusted origin의 근거가 아니다. sourceAllowed는 두 header를 읽지 않는다.
func TestSourceAllowedIgnoresHostHeaders(t *testing.T) {
	h := http.Header{
		"Origin":           {"https://evil.example.com"},
		"Host":             {"evil.example.com"},
		"X-Forwarded-Host": {"evil.example.com"},
	}
	if sourceAllowed("https://labbit.example.com", h) {
		t.Fatal("Host/X-Forwarded-Host와 같은 Origin을 허용하면 안 됩니다")
	}
}

func TestNormalizeOriginIsIdempotent(t *testing.T) {
	first, err := ParseOrigin("HTTPS://Labbit.Example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseOrigin(first)
	if err != nil || second != first {
		t.Fatalf("ParseOrigin(%q) = (%q, %v), want %q", first, second, err, first)
	}
}

func header(key, value string) http.Header {
	h := http.Header{}
	h[http.CanonicalHeaderKey(key)] = []string{value}
	return h
}
