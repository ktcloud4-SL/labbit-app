package workspacefile

import (
	"context"
	"regexp"

	"github.com/google/uuid"
)

// Revision은 파일 한 시점의 opaque revision이다. 생성 방식과 형식은 호환성 계약이 아니며 이 package는 값을 해석하지 않는다.
// 만드는 쪽은 Connector이고, HTTP ETag로 안전하게 전달할 수 있도록 file-data.schema.json의 Revision(문자 집합과 길이)만 지킨다.
type Revision string

// revisionPattern은 contracts/connector/file-data.schema.json의 Revision pattern과 같다.
var revisionPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// ParseRevision은 raw가 Connector 계약이 허용하는 revision 형식이면 Revision을 반환한다.
func ParseRevision(raw string) (Revision, bool) {
	if !revisionPattern.MatchString(raw) {
		return "", false
	}
	return Revision(raw), true
}

// EntryKind는 디렉터리 항목의 종류다. contracts의 kind와 같다.
type EntryKind string

const (
	KindFile      EntryKind = "file"
	KindDirectory EntryKind = "directory"
)

// Entry는 디렉터리의 직계 항목 하나다. Name은 아직 경로 규칙을 검증하지 않은 Connector의 값이다.
type Entry struct {
	Name string
	Kind EntryKind
}

// FileData는 Read의 결과다. Content는 Connector가 보낸 byte이며 UTF-8 text인지는 Service가 판단한다.
type FileData struct {
	Content  []byte
	Revision Revision
}

// Target은 Service가 짧은 transaction에서 결정한 resolved Workspace VM이다. Browser가 보낸 값이 아니다.
// Transport는 이 값으로 Connector에 요청하고, 응답이 같은 값을 가리키는지 확인한다.
type Target struct {
	LabInstanceID uuid.UUID
	// Generation은 이 target을 결정한 시점의 LabInstance generation이다(1 이상).
	Generation  int64
	ConnectorID uuid.UUID
	// WorkspaceVMKey는 CreationSnapshot의 workspaceVmKey, ProviderServerID는 현재 generation의 PRESENT SERVER ProviderResource다.
	WorkspaceVMKey   string
	ProviderServerID string
	// RequestID는 원본 HTTP request의 correlation이다(선택). 파일 경로나 본문이 아니다.
	RequestID string
}

// Transport는 Workspace VM의 파일을 읽고 쓰는 external port다. Connector adapter가 구현한다.
// 범용 filesystem abstraction이 아니라 MVP의 세 동작만 갖는다.
//
// 모든 method는 Service가 검증한 canonical Path만 받으며 경로를 다시 해석하거나 정규화하지 않는다.
// 오류는 이 package의 sentinel(ErrPathNotFound, ErrNotAFile, ErrNotADirectory, ErrFilePermissionDenied, ErrTooLarge,
// ErrStaleRevision, ErrConnectorUnavailable, ErrTransportUnavailable, ErrSaveOutcomeUnknown)로 돌려주며 Connector가 보낸 문구와
// SSH/SFTP raw 오류를 오류 문자열에 넣지 않는다. ctx가 끝나면 진행 중인 작업을 정리하고 ctx.Err()를 반환한다.
type Transport interface {
	// Tree는 dir의 직계 항목을 반환한다. 재귀하지 않는다.
	Tree(ctx context.Context, target Target, dir Path) ([]Entry, error)
	// Read는 파일 본문과 그 시점의 revision을 반환한다. 파일이 maxBytes를 넘으면 본문 없이 ErrTooLarge다.
	Read(ctx context.Context, target Target, file Path, maxBytes int64) (FileData, error)
	// Save는 현재 revision이 expected와 같을 때만 파일을 교체하고 새 revision을 반환한다. 다르면 파일을 바꾸지 않고 ErrStaleRevision이며,
	// 파일이 없으면 만들지 않고 ErrPathNotFound다. 요청을 보낸 뒤 결과를 받지 못하면 ErrSaveOutcomeUnknown이며 자동으로 다시 보내지 않는다.
	Save(ctx context.Context, target Target, file Path, expected Revision, content []byte) (Revision, error)
}
