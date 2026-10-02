package terminal

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
)

const (
	// AttachTokenLifetime은 attach token의 절대 만료 시간이다(발급 후 8시간). 주기적 회전이나 갱신은 하지 않는다.
	AttachTokenLifetime = 8 * time.Hour

	// attachTokenBytes는 CSPRNG로 생성하는 raw token의 길이다.
	attachTokenBytes = 32
)

// newAttachToken은 CSPRNG raw token 32 bytes를 만들고 Browser용 encoding(padding 없는 canonical Base64URL)과
// 저장용 SHA-256 digest를 함께 반환한다. raw token은 이 반환값 외에는 어디에도 저장하지 않는다.
func newAttachToken() (realtime.AttachToken, [sha256.Size]byte) {
	var raw [attachTokenBytes]byte
	// crypto/rand.Read는 실패하지 않고 process를 중단시키므로 반환 오류를 처리하지 않는다.
	_, _ = rand.Read(raw[:])
	return realtime.AttachToken(base64.RawURLEncoding.EncodeToString(raw[:])), sha256.Sum256(raw[:])
}

// attachTokenDigest는 Browser가 제시한 token을 raw 32 bytes로 되돌려 SHA-256 digest를 계산한다.
// 형식이 맞지 않거나 canonical encoding이 아니면 저장소를 조회하지 않도록 false를 반환한다.
//
// Go의 base64 decoder는 '\r'와 '\n'을 무시하므로 Strict()만으로는 끝에 개행이 붙은 값이 같은 token으로 통과한다.
// 그래서 decode한 값을 다시 encode해 입력과 정확히 같을 때만(= canonical 표기일 때만) 받아들인다.
func attachTokenDigest(token realtime.AttachToken) ([sha256.Size]byte, bool) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(string(token))
	if err != nil || len(raw) != attachTokenBytes || base64.RawURLEncoding.EncodeToString(raw) != string(token) {
		return [sha256.Size]byte{}, false
	}
	return sha256.Sum256(raw), true
}
