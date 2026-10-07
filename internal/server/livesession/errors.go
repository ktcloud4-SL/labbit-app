package livesession

import "errors"

var (
	// ErrNotFound는 LiveSession, TerminalSession, Class 등이 존재하지 않거나 ID 형식이 올바르지 않음이다.
	ErrNotFound = errors.New("livesession: 찾을 수 없음")
	// ErrForbidden은 현재 사용자에게 권한이 없음이다 (다른 Organization, 강사/학생 역할 불일치, 소유자 아님).
	ErrForbidden = errors.New("livesession: 접근 권한 없음")
	// ErrLiveActive는 해당 Class에 이미 active 상태인 LiveSession이 존재함이다.
	ErrLiveActive = errors.New("livesession: 이미 활성화된 LiveSession이 존재함")
	// ErrSourceUnavailable은 source TerminalSession이 유효하지 않거나(ENDED, OPENING 등) Relay에서 사용할 수 없음이다.
	ErrSourceUnavailable = errors.New("livesession: source TerminalSession을 사용할 수 없음")
	// ErrUnavailable은 Live 기능 또는 Relay를 사용할 수 없는 배포 구성이다.
	ErrUnavailable = errors.New("livesession: Live Relay를 사용할 수 없음")
	// ErrInconsistentData는 DB에 저장된 관계가 서로 모순됨이다.
	ErrInconsistentData = errors.New("livesession: 저장된 데이터가 모순됨")
)
