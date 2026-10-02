package terminal

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
)

func TestNewAttachTokenIsCSPRNGThirtyTwoBytesBase64URL(t *testing.T) {
	token, digest := newAttachToken()

	// padding 없는 canonical Base64URL이고 32 bytes로 되돌아간다.
	if strings.ContainsAny(string(token), "=+/ \n") || len(token) != 43 {
		t.Fatalf("token 형식 = %q (len %d), want 43자 padding 없는 Base64URL", token, len(token))
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(string(token))
	if err != nil || len(raw) != 32 {
		t.Fatalf("decode = %d bytes, error = %v, want 32 bytes", len(raw), err)
	}
	// 저장하는 것은 raw token의 SHA-256 digest다.
	if want := sha256.Sum256(raw); digest != want {
		t.Fatal("digest가 SHA-256(raw token)이 아님")
	}
	if got, ok := attachTokenDigest(token); !ok || got != digest {
		t.Fatalf("attachTokenDigest(token) = %x, %v, want %x", got, ok, digest)
	}

	// 발급할 때마다 다르다. 이전 token을 재사용하지 않는다.
	seen := map[realtime.AttachToken]bool{token: true}
	for range 200 {
		next, nextDigest := newAttachToken()
		if seen[next] {
			t.Fatalf("token이 중복됨: %q", next)
		}
		seen[next] = true
		if nextDigest == digest {
			t.Fatal("digest가 중복됨")
		}
	}
}

// Browser가 제시한 token은 canonical 형식이어야 digest를 계산한다. 형식이 다르면 저장소를 조회하지 않는다.
func TestAttachTokenDigestRejectsNonCanonicalTokens(t *testing.T) {
	token, _ := newAttachToken()
	raw, _ := base64.RawURLEncoding.DecodeString(string(token))

	// 마지막 글자는 6비트 중 앞 4비트만 의미가 있다. 남는 2비트가 0이 아닌 표기는 canonical이 아니다.
	alphabet := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	last := strings.IndexByte(alphabet, token[len(token)-1])
	nonCanonical := string(token[:len(token)-1]) + string(alphabet[(last&^3)|1])
	if nonCanonical == string(token) {
		nonCanonical = string(token[:len(token)-1]) + string(alphabet[(last&^3)|2])
	}

	tests := map[string]string{
		"empty":                 "",
		"padded":                base64.URLEncoding.EncodeToString(raw),
		"standard alphabet":     base64.RawStdEncoding.EncodeToString(append([]byte{0xfb, 0xff}, raw[2:]...)),
		"31 bytes":              base64.RawURLEncoding.EncodeToString(raw[:31]),
		"33 bytes":              base64.RawURLEncoding.EncodeToString(append(append([]byte{}, raw...), 1)),
		"leading space":         " " + string(token),
		"trailing newline":      string(token) + "\n",
		"embedded newline":      string(token[:10]) + "\n" + string(token[10:]),
		"non-canonical trailer": nonCanonical,
		"not base64":            "!!!!",
		"uppercase changed":     strings.ToLower(string(token)) + "x",
	}
	for name, raw := range tests {
		if _, ok := attachTokenDigest(realtime.AttachToken(raw)); ok {
			t.Errorf("%s: %q가 token으로 인정됨", name, raw)
		}
	}
}

func TestAttachTokenLifetimeIsEightHours(t *testing.T) {
	if AttachTokenLifetime != 8*time.Hour {
		t.Fatalf("AttachTokenLifetime = %v, want 8h", AttachTokenLifetime)
	}
}
