package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// Argon2id 생성 baseline이다. 원본은 docs/backend/auth-session.md의 "Password 저장과 검증"이다.
// 검증은 저장된 PHC 안의 parameter를 사용하므로 향후 cost를 올려도 기존 hash가 계속 검증된다.
const (
	argon2MemoryKiB   = 19 * 1024
	argon2Iterations  = 2
	argon2Parallelism = 1
	argon2SaltLength  = 16
	argon2KeyLength   = 32
)

// 저장된 PHC가 손상되었을 때 process 자원을 소진하지 않도록 하는 sanity 상한/하한이다.
// 새 Password 정책이 아니며 argon2 reference 구현의 최소값과 운영 가능한 상한만 둔다.
const (
	maxArgon2MemoryKiB   = 1 << 20 // 1 GiB
	maxArgon2Iterations  = 16
	maxArgon2Parallelism = 16
	minArgon2SaltLength  = 8
	maxArgon2SaltLength  = 1024
	minArgon2KeyLength   = 4
	maxArgon2KeyLength   = 1024
)

// dummyPasswordHash는 존재하지 않는 username의 로그인에서 실제 계정과 같은 Argon2id 검증 비용을
// 치르기 위한 고정 PHC다. 같은 baseline parameter를 사용하며 어떤 사용자 Password나 계정에서도 파생하지 않았다.
// 생성에 쓴 Password는 무작위 값이며 폐기했으므로 어떤 입력도 일치하지 않는다. Secret이 아니다.
const dummyPasswordHash repository.PasswordHash = "$argon2id$v=19$m=19456,t=2,p=1$UCrfVZY+cVyXR2TtxeJY1Q$PUeDOunDMjbq1/cpOOZcNWafh+qEIa6BBppsztdf42A"

// ErrMalformedPasswordHash는 저장된 값이 지원하는 Argon2id PHC 형식이 아님을 나타낸다.
// 메시지에 hash 값을 포함하지 않는다.
var ErrMalformedPasswordHash = errors.New("auth: 지원하지 않는 password hash 형식")

// PasswordVerifier는 Argon2id PHC 검증 경계다. 불일치는 (false, nil)이고, PHC를 사용할 수 없을 때만 오류다.
type PasswordVerifier interface {
	Verify(hash repository.PasswordHash, password string) (bool, error)
}

// Argon2id는 x/crypto의 Argon2id로 PasswordVerifier를 구현한다.
type Argon2id struct{}

var _ PasswordVerifier = Argon2id{}

// Verify는 hash에 기록된 parameter와 salt로 password를 다시 계산해 상수 시간으로 비교한다.
func (Argon2id) Verify(hash repository.PasswordHash, password string) (bool, error) {
	parsed, err := parsePHC(string(hash))
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), parsed.salt, parsed.iterations, parsed.memoryKiB, parsed.parallelism, uint32(len(parsed.key)))
	return subtle.ConstantTimeCompare(got, parsed.key) == 1, nil
}

// HashPassword는 생성 baseline으로 새 Argon2id PHC를 만든다. salt는 CSPRNG로 생성한다.
func HashPassword(password string) (repository.PasswordHash, error) {
	salt := make([]byte, argon2SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: salt 생성: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argon2Iterations, argon2MemoryKiB, argon2Parallelism, argon2KeyLength)
	return repository.PasswordHash(fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argon2MemoryKiB, argon2Iterations, argon2Parallelism,
		phcEncoding.EncodeToString(salt), phcEncoding.EncodeToString(key),
	)), nil
}

// phcEncoding은 PHC string format의 padding 없는 표준 base64다.
var phcEncoding = base64.RawStdEncoding.Strict()

type phc struct {
	memoryKiB   uint32
	iterations  uint32
	parallelism uint8
	salt        []byte
	key         []byte
}

// parsePHC는 "$argon2id$v=19$m=<KiB>,t=<n>,p=<n>$<salt>$<key>" 형식만 받아들인다.
func parsePHC(raw string) (phc, error) {
	parts := strings.Split(raw, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return phc{}, ErrMalformedPasswordHash
	}

	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return phc{}, ErrMalformedPasswordHash
	}
	memory, ok1 := parseParam(params[0], "m=", maxArgon2MemoryKiB)
	iterations, ok2 := parseParam(params[1], "t=", maxArgon2Iterations)
	parallelism, ok3 := parseParam(params[2], "p=", maxArgon2Parallelism)
	if !ok1 || !ok2 || !ok3 || iterations < 1 || parallelism < 1 || memory < 8*parallelism {
		return phc{}, ErrMalformedPasswordHash
	}

	salt, err := phcEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < minArgon2SaltLength || len(salt) > maxArgon2SaltLength {
		return phc{}, ErrMalformedPasswordHash
	}
	key, err := phcEncoding.DecodeString(parts[5])
	if err != nil || len(key) < minArgon2KeyLength || len(key) > maxArgon2KeyLength {
		return phc{}, ErrMalformedPasswordHash
	}

	return phc{
		memoryKiB:   uint32(memory),
		iterations:  uint32(iterations),
		parallelism: uint8(parallelism),
		salt:        salt,
		key:         key,
	}, nil
}

// parseParam은 "<prefix><10진수>"를 limit 이하의 값으로 읽는다. 부호나 공백은 허용하지 않는다.
func parseParam(s, prefix string, limit uint64) (uint64, bool) {
	digits, found := strings.CutPrefix(s, prefix)
	if !found {
		return 0, false
	}
	n, err := strconv.ParseUint(digits, 10, 32)
	if err != nil || n > limit {
		return 0, false
	}
	return n, true
}
