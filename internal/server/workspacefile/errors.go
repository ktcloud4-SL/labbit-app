package workspacefile

import "errors"

// Application error다. HTTP status를 알지 못하며 httpapi가 OpenAPI의 status와 Problem Details로 바꾼다.
// 오류 문구에는 경로, 파일 본문, Connector가 보낸 문구를 넣지 않는다.
var (
	// ErrNotFound는 LabInstance가 없음이다. 이 서버가 발급하지 않은 형식의 ID도 구분하지 않는다.
	ErrNotFound = errors.New("workspacefile: 찾을 수 없음")
	// ErrForbidden은 대상은 있지만 현재 사용자가 사용할 수 없음이다(다른 사용자·다른 Organization의 것, 현재 ClassMembership 없음).
	ErrForbidden = errors.New("workspacefile: 접근 권한 없음")
	// ErrLabInstanceNotReady는 LabInstance가 READY가 아니라 Workspace file을 사용할 수 없음이다.
	ErrLabInstanceNotReady = errors.New("workspacefile: LabInstance가 READY가 아님")
	// ErrTargetUnavailable은 현재 generation에 Workspace VM의 PRESENT SERVER ProviderResource가 없음이다.
	ErrTargetUnavailable = errors.New("workspacefile: Workspace VM을 사용할 수 없음")
	// ErrTargetChanged는 요청을 처리하는 동안 Workspace VM의 generation이 바뀌었거나 LabInstance가 READY를 벗어났음이다(Reset race).
	// 결과를 성공으로 처리하지 않는다.
	ErrTargetChanged = errors.New("workspacefile: 처리 중 Workspace VM이 바뀜")
	// ErrInconsistentData는 저장소가 반환한 관계가 서로 모순됨이다. 권한 상태 중 하나로 해석하지 않고 내부 오류로 취급한다(fail closed).
	ErrInconsistentData = errors.New("workspacefile: 저장된 관계가 서로 모순됨")

	// ErrPathNotFound는 Workspace에 그 파일이나 디렉터리가 없음이다.
	ErrPathNotFound = errors.New("workspacefile: 경로가 없음")
	// ErrNotAFile은 읽거나 저장할 경로가 일반 파일이 아님이다.
	ErrNotAFile = errors.New("workspacefile: 일반 파일이 아님")
	// ErrNotADirectory는 목록을 조회할 경로가 디렉터리가 아님이다.
	ErrNotADirectory = errors.New("workspacefile: 디렉터리가 아님")
	// ErrFilePermissionDenied는 Workspace VM의 파일 시스템 권한이 접근을 거절했음이다. Labbit의 권한 판정(ErrForbidden)과 다르다.
	ErrFilePermissionDenied = errors.New("workspacefile: VM 파일 시스템 권한 거절")
	// ErrTooLarge는 파일이 설정된 크기 한도를 넘음이다.
	ErrTooLarge = errors.New("workspacefile: 파일이 크기 한도를 넘음")
	// ErrDirectoryTooLarge는 디렉터리 목록이 전송 한도를 넘음이다.
	ErrDirectoryTooLarge = errors.New("workspacefile: 디렉터리 목록이 한도를 넘음")
	// ErrUnsupportedEncoding은 본문이 유효한 UTF-8이 아님이다.
	ErrUnsupportedEncoding = errors.New("workspacefile: UTF-8이 아님")
	// ErrBinaryContent는 본문에 NUL이 있어 text가 아님이다.
	ErrBinaryContent = errors.New("workspacefile: binary 내용")
	// ErrStaleRevision은 If-Match의 revision이 현재 파일의 revision과 다름이다. 파일을 덮어쓰지 않았다.
	ErrStaleRevision = errors.New("workspacefile: revision이 현재와 다름")

	// ErrConnectorUnavailable은 Connector의 Control connection을 사용할 수 없음이다(없음, HELLO_ACK 전, 종료 중).
	ErrConnectorUnavailable = errors.New("workspacefile: Connector를 사용할 수 없음")
	// ErrTransportUnavailable은 Connector가 File transport를 지원하지 않거나, 시간 안에 응답하지 않거나, protocol을 위반했거나,
	// Workspace VM/SFTP를 사용할 수 없음이다.
	ErrTransportUnavailable = errors.New("workspacefile: File transport를 사용할 수 없음")
	// ErrSaveOutcomeUnknown은 Save 요청을 Connector에 보낸 뒤 결과를 받지 못해 저장 여부를 알 수 없음이다. 자동으로 다시 보내지 않는다.
	ErrSaveOutcomeUnknown = errors.New("workspacefile: 저장 여부를 알 수 없음")
)
