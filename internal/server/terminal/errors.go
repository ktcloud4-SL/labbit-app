package terminal

import "errors"

// Application error다. HTTP status를 알지 못하며 httpapi가 OpenAPI의 status와 Problem Details로 바꾼다.
var (
	// ErrNotFound는 LabInstance나 TerminalSession이 없음이다. 이 서버가 발급하지 않은 형식의 ID도 구분하지 않는다.
	ErrNotFound = errors.New("terminal: 찾을 수 없음")
	// ErrForbidden은 대상은 있지만 현재 사용자가 사용할 수 없음이다(다른 사용자·다른 Organization의 것, 현재 ClassMembership 없음).
	ErrForbidden = errors.New("terminal: 접근 권한 없음")
	// ErrLabInstanceNotReady는 LabInstance가 READY가 아니라 TerminalSession을 만들 수 없음이다.
	ErrLabInstanceNotReady = errors.New("terminal: LabInstance가 READY가 아님")
	// ErrTargetNotFound는 targetVmKey가 LabInstance의 immutable CreationSnapshot에 있는 VM이 아님이다(Targets가 돌려주지 않은 key).
	ErrTargetNotFound = errors.New("terminal: 대상 VM을 찾을 수 없음")
	// ErrTargetUnavailable은 targetVmKey는 CreationSnapshot의 VM이지만 현재 generation에 PRESENT SERVER ProviderResource가 없어
	// 지금 사용할 수 없음이다.
	ErrTargetUnavailable = errors.New("terminal: 대상 VM을 사용할 수 없음")
	// ErrConnectorUnavailable은 Connector의 Control connection을 사용할 수 없어 TERMINAL_OPEN을 보내지 못했음이다.
	ErrConnectorUnavailable = errors.New("terminal: Connector를 사용할 수 없음")
	// ErrOpenFailed는 Connector가 PTY를 준비하지 못했거나, 시간 안에 끝나지 않았거나, Terminal Data WSS가 실제로 bind되지 않았음이다.
	ErrOpenFailed = errors.New("terminal: TerminalSession을 열지 못함")
	// ErrUnavailable은 이 배포 구성에 Terminal Relay가 없음이다(예: realtime role 없이 api만 enabled).
	ErrUnavailable = errors.New("terminal: Terminal Relay를 사용할 수 없음")
	// ErrInconsistentData는 저장소가 반환한 관계가 서로 모순됨이다. 권한 상태 중 하나로 해석하지 않고 내부 오류로 취급한다(fail closed).
	ErrInconsistentData = errors.New("terminal: 저장된 관계가 서로 모순됨")
	// ErrInvalidReason은 지원하지 않는 종료 원인이다.
	ErrInvalidReason = errors.New("terminal: 지원하지 않는 종료 원인")
)
