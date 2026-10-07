package previewsession

import "errors"

var (
	// ErrNotFound는 LabInstance나 PreviewSession이 없거나 ID 형식이 올바르지 않음이다.
	ErrNotFound = errors.New("previewsession: 찾을 수 없음")
	// ErrForbidden는 현재 사용자의 LabInstance/PreviewSession이 아니거나 ClassMembership이 없음이다.
	ErrForbidden = errors.New("previewsession: 권한 없음")
	// ErrLabInstanceNotReady는 LabInstance가 READY가 아님이다.
	ErrLabInstanceNotReady = errors.New("previewsession: LabInstance가 READY가 아님")
	// ErrTargetUnavailable은 현재 generation에 Workspace VM의 PRESENT SERVER ProviderResource가 없음이다.
	ErrTargetUnavailable = errors.New("previewsession: Workspace VM을 사용할 수 없음")
	// ErrTargetChanged는 PreviewSession을 만드는 동안 대상의 generation이 바뀌었거나 LabInstance가 READY를 벗어났음이다.
	// 오래된 target을 성공으로 돌려주지 않는다.
	ErrTargetChanged = errors.New("previewsession: 처리 중 대상이 바뀜")
	// ErrInconsistentData는 저장된 CreationSnapshot/ProviderResource 관계가 모순임이다. 값을 만들어 채우지 않고 fail closed한다.
	ErrInconsistentData = errors.New("previewsession: 저장된 데이터가 모순됨")
	// ErrInvalidPort는 targetPort가 1~65535의 정수가 아님이다.
	ErrInvalidPort = errors.New("previewsession: targetPort가 올바르지 않음")
	// ErrPortNotAllowed는 targetPort가 Backend의 명시적 허용 목록에 없거나 SSH 관리 port임이다.
	ErrPortNotAllowed = errors.New("previewsession: targetPort가 허용되지 않음")

	// ErrConnectorUnavailable은 Connector의 Control connection이 없음이다.
	ErrConnectorUnavailable = errors.New("previewsession: Connector를 사용할 수 없음")
	// ErrTransportUnsupported는 Connector가 연결되어 있지만 preview-v1을 선언하지 않았음이다.
	ErrTransportUnsupported = errors.New("previewsession: Connector가 Preview transport를 지원하지 않음")
	// ErrAppNotRunning은 Connector가 APP_NOT_RUNNING을 보고했음이다(Workspace VM에는 도달했지만 그 port에서 listen하는 application이 없음).
	ErrAppNotRunning = errors.New("previewsession: application이 실행 중이 아님")
	// ErrPortRejected는 Connector가 PORT_REJECTED를 보고했음이다.
	ErrPortRejected = errors.New("previewsession: Connector가 port를 거절함")
	// ErrTargetUnreachable는 Connector가 VM_UNREACHABLE을 보고했음이다.
	ErrTargetUnreachable = errors.New("previewsession: Workspace VM에 도달할 수 없음")
	// ErrOpenTimeout은 Connector가 시간 안에 PreviewSession을 열지 못했음이다.
	ErrOpenTimeout = errors.New("previewsession: Connector가 시간 안에 열지 못함")
	// ErrOpenFailed는 그 밖에 Connector가 PreviewSession을 열지 못했음이다.
	ErrOpenFailed = errors.New("previewsession: PreviewSession을 열지 못함")
	// ErrUnavailable은 이 process가 PreviewSession을 만들 수 없음이다(종료 중).
	ErrUnavailable = errors.New("previewsession: 사용할 수 없음")

	// ErrInvalidReason은 CloseForLabMutation의 reason이 LAB_RESET/LAB_CLEANUP이 아님이다.
	ErrInvalidReason = errors.New("previewsession: 종료 reason이 올바르지 않음")
)
