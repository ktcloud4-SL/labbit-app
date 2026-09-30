package auth

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func TestHashPasswordProducesBaselinePHCThatVerifies(t *testing.T) {
	hash, err := HashPassword(correctPassword)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	parsed, err := parsePHC(string(hash))
	if err != nil {
		t.Fatalf("생성한 PHC를 다시 읽지 못했습니다: %v", err)
	}
	assertBaseline(t, parsed)

	verifier := Argon2id{}
	if ok, err := verifier.Verify(hash, correctPassword); err != nil || !ok {
		t.Fatalf("Verify(correct) = (%v, %v), want (true, nil)", ok, err)
	}
	if ok, err := verifier.Verify(hash, wrongPassword); err != nil || ok {
		t.Fatalf("Verify(wrong) = (%v, %v), want (false, nil)", ok, err)
	}
	if ok, err := verifier.Verify(hash, ""); err != nil || ok {
		t.Fatalf("Verify(empty) = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestHashPasswordUsesFreshSalt(t *testing.T) {
	a, err := HashPassword(correctPassword)
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword(correctPassword)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("같은 Password라도 salt가 달라 hash가 달라야 합니다")
	}
}

// 검증은 생성 baseline이 아니라 저장된 PHC 안의 parameter를 사용해야 향후 cost 상승과 호환된다.
func TestVerifyUsesParametersStoredInPHC(t *testing.T) {
	salt := []byte("0123456789abcdef")
	key := argon2.IDKey([]byte(correctPassword), salt, 1, 1024, 2, 16)
	hash := repository.PasswordHash(fmt.Sprintf("$argon2id$v=19$m=1024,t=1,p=2$%s$%s",
		phcEncoding.EncodeToString(salt), phcEncoding.EncodeToString(key)))

	if ok, err := (Argon2id{}).Verify(hash, correctPassword); err != nil || !ok {
		t.Fatalf("Verify(correct, custom parameters) = (%v, %v), want (true, nil)", ok, err)
	}
	if ok, err := (Argon2id{}).Verify(hash, wrongPassword); err != nil || ok {
		t.Fatalf("Verify(wrong, custom parameters) = (%v, %v), want (false, nil)", ok, err)
	}
}

// dummy PHC는 실제 계정과 같은 baseline 비용을 치러야 username 존재 여부가 수행 시간으로 드러나지 않는다.
func TestDummyPasswordHashMatchesBaselineAndNeverMatches(t *testing.T) {
	parsed, err := parsePHC(string(dummyPasswordHash))
	if err != nil {
		t.Fatalf("dummy PHC를 읽지 못했습니다: %v", err)
	}
	assertBaseline(t, parsed)

	for _, password := range []string{"", correctPassword, wrongPassword, string(dummyPasswordHash)} {
		ok, err := (Argon2id{}).Verify(dummyPasswordHash, password)
		if err != nil || ok {
			t.Fatalf("Verify(dummy, %q) = (%v, %v), want (false, nil)", password, ok, err)
		}
	}
}

func assertBaseline(t *testing.T, parsed phc) {
	t.Helper()
	if parsed.memoryKiB != 19*1024 || parsed.iterations != 2 || parsed.parallelism != 1 {
		t.Errorf("parameters = m=%d,t=%d,p=%d, want m=19456,t=2,p=1", parsed.memoryKiB, parsed.iterations, parsed.parallelism)
	}
	if len(parsed.salt) < 16 {
		t.Errorf("salt = %d bytes, want at least 16", len(parsed.salt))
	}
	if len(parsed.key) != 32 {
		t.Errorf("derived key = %d bytes, want 32", len(parsed.key))
	}
}

func TestVerifyRejectsMalformedPHC(t *testing.T) {
	salt := phcEncoding.EncodeToString([]byte("0123456789abcdef"))
	key := phcEncoding.EncodeToString(make([]byte, 32))
	valid := fmt.Sprintf("$argon2id$v=19$m=19456,t=2,p=1$%s$%s", salt, key)

	tests := map[string]string{
		"empty":                "",
		"not a PHC":            "plain-text-password",
		"argon2i":              strings.Replace(valid, "argon2id", "argon2i", 1),
		"bcrypt":               "$2a$10$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ01234",
		"missing version":      fmt.Sprintf("$argon2id$m=19456,t=2,p=1$%s$%s", salt, key),
		"old version":          strings.Replace(valid, "v=19", "v=16", 1),
		"reordered parameters": fmt.Sprintf("$argon2id$v=19$t=2,m=19456,p=1$%s$%s", salt, key),
		"missing parameter":    fmt.Sprintf("$argon2id$v=19$m=19456,t=2$%s$%s", salt, key),
		"signed parameter":     strings.Replace(valid, "t=2", "t=+2", 1),
		"zero iterations":      strings.Replace(valid, "t=2", "t=0", 1),
		"zero parallelism":     strings.Replace(valid, "p=1", "p=0", 1),
		"memory below 8*p":     strings.Replace(valid, "m=19456", "m=4", 1),
		"memory above ceiling": strings.Replace(valid, "m=19456", "m=4294967295", 1),
		"memory beyond 32 bit": strings.Replace(valid, "m=19456", "m=99999999999", 1),
		"parallelism 256":      strings.Replace(valid, "p=1", "p=256", 1),
		"iterations ceiling":   strings.Replace(valid, "t=2", "t=1000000", 1),
		"parallelism ceiling":  strings.Replace(valid, "p=1", "p=255", 1),
		"salt not base64":      fmt.Sprintf("$argon2id$v=19$m=19456,t=2,p=1$***$%s", key),
		"salt too short":       fmt.Sprintf("$argon2id$v=19$m=19456,t=2,p=1$%s$%s", phcEncoding.EncodeToString([]byte("short")), key),
		"padded salt":          fmt.Sprintf("$argon2id$v=19$m=19456,t=2,p=1$%s$%s", base64.StdEncoding.EncodeToString([]byte("0123456789abcd")), key),
		"key too short":        fmt.Sprintf("$argon2id$v=19$m=19456,t=2,p=1$%s$%s", salt, phcEncoding.EncodeToString([]byte("abc"))),
		"trailing segment":     valid + "$extra",
	}

	for name, hash := range tests {
		t.Run(name, func(t *testing.T) {
			ok, err := (Argon2id{}).Verify(repository.PasswordHash(hash), correctPassword)

			if ok || !errors.Is(err, ErrMalformedPasswordHash) {
				t.Fatalf("Verify() = (%v, %v), want (false, ErrMalformedPasswordHash)", ok, err)
			}
			if hash != "" && strings.Contains(err.Error(), hash) {
				t.Fatalf("오류가 hash 원문을 포함합니다: %v", err)
			}
		})
	}
}
