// Package workspacefile은 본인 LabInstance의 Workspace VM 파일 Tree/Read/Save use case다(LBT-100).
//
// Browser가 보낸 경로만 신뢰하지 않고(path.go의 단일 canonical 규칙) 대상 VM도 신뢰하지 않는다. 현재 User, LabInstance 소유,
// 현재 ClassMembership, READY, 그 LabExecution의 immutable CreationSnapshot의 workspaceVmKey, 현재 generation의 PRESENT SERVER
// ProviderResource를 PostgreSQL 상태로 결정하고 Browser가 보낸 VM key, Provider Server ID, Connector ID, generation은 받지 않는다.
//
// 이 package는 Connector와 SSH/SFTP를 알지 못한다. 파일 I/O는 Transport port로만 하며 PostgreSQL transaction 안에서는 하지 않는다.
// 권한과 대상은 짧은 transaction에서 결정해 commit한 뒤 Transport를 호출하고, 성공 결과를 돌려주기 전에 대상이 그대로인지 다시 확인한다.
//
// 파일 본문과 경로는 저장하거나 log에 남기지 않는다. HTTP status와 Problem Details는 알지 못한다.
package workspacefile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

const (
	// LabInstanceStatusReady는 Workspace file을 사용할 수 있는 유일한 LabInstance 상태다.
	LabInstanceStatusReady = "READY"
	// ProviderResourcePresent는 대상 VM이 지금 존재하는 provider_resources.lifecycle_status다.
	ProviderResourcePresent = "PRESENT"

	// DefaultMaxFileBytes는 Options.MaxFileBytes가 0일 때 쓰는 보수적인 기본 한도다. 장기 계약이 아니며 계약 문서에 고정하지 않는다.
	DefaultMaxFileBytes int64 = 1 << 20
)

// Store는 Service가 사용하는 persistence 경계다. postgres.Store가 구현한다.
type Store interface {
	repository.Transactor
	// LabInstanceByID는 LabInstance를 잠그지 않고 읽는다. Transport 호출 뒤 대상이 그대로인지 확인하는 데 쓴다.
	LabInstanceByID(ctx context.Context, id uuid.UUID) (repository.LabInstance, error)
}

// Options는 Service 구성이다.
type Options struct {
	Store     Store
	Transport Transport
	// MaxFileBytes는 Read/Save가 받아들이는 파일 본문의 최대 byte 수다. 0이면 DefaultMaxFileBytes다. test에서 주입할 수 있다.
	MaxFileBytes int64
	// Logger가 nil이면 로그를 남기지 않는다. 경로와 파일 본문은 어떤 경우에도 기록하지 않는다.
	Logger *slog.Logger
}

// Service는 Workspace file use case다.
type Service struct {
	store     Store
	transport Transport
	maxBytes  int64
	logger    *slog.Logger
}

// NewService는 Service를 만든다.
func NewService(opts Options) (*Service, error) {
	if opts.Store == nil || opts.Transport == nil {
		return nil, errors.New("workspacefile: Store와 Transport가 필요합니다")
	}
	if opts.MaxFileBytes < 0 {
		return nil, errors.New("workspacefile: MaxFileBytes는 0 이상이어야 합니다")
	}
	s := &Service{store: opts.Store, transport: opts.Transport, maxBytes: opts.MaxFileBytes, logger: opts.Logger}
	if s.maxBytes == 0 {
		s.maxBytes = DefaultMaxFileBytes
	}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	return s, nil
}

// MaxFileBytes는 이 Service가 받아들이는 파일 본문의 최대 byte 수다. HTTP layer가 요청 body 상한을 정할 때 쓴다.
func (s *Service) MaxFileBytes() int64 { return s.maxBytes }

// TreeInput은 Tree의 입력이다. 모든 값은 검증되지 않았다.
type TreeInput struct {
	LabInstanceID string
	// Path는 빈 문자열이면 Workspace root다.
	Path string
	// RequestID는 원본 HTTP request의 correlation이다(선택).
	RequestID string
}

// Item은 Tree의 항목 하나다. Path는 canonical이며 그대로 Read/Tree에 다시 보낼 수 있다.
type Item struct {
	Name string
	Path string
	Kind EntryKind
}

// Listing은 Tree의 결과다. Items는 Name의 byte 순서 오름차순이다.
type Listing struct {
	Path  string
	Items []Item
}

// Tree는 현재 사용자의 Workspace에서 디렉터리 하나의 직계 항목을 반환한다.
func (s *Service) Tree(ctx context.Context, user repository.User, in TreeInput) (Listing, error) {
	dir, err := ParseDirectory(in.Path)
	if err != nil {
		return Listing{}, err
	}
	target, err := s.resolve(ctx, user, in.LabInstanceID, in.RequestID)
	if err != nil {
		return Listing{}, err
	}

	entries, err := s.transport.Tree(ctx, target, dir)
	if errors.Is(err, ErrTooLarge) {
		err = ErrDirectoryTooLarge
	}
	if err != nil {
		return Listing{}, err
	}
	if err := s.stillCurrent(ctx, target); err != nil {
		return Listing{}, err
	}

	items := make([]Item, 0, len(entries))
	dropped := 0
	for _, entry := range entries {
		if entry.Kind != KindFile && entry.Kind != KindDirectory {
			dropped++
			continue
		}
		// 경로 규칙으로 표현할 수 없는 이름(백슬래시, 제어 문자, percent-escape 형태 등)은 열 수도 없으므로 목록에서 제외한다.
		// 그 때문에 목록 전체를 실패시키지 않는다. 이름은 기록하지 않는다.
		child, ok := dir.child(entry.Name)
		if !ok {
			dropped++
			continue
		}
		items = append(items, Item{Name: entry.Name, Path: child.String(), Kind: entry.Kind})
	}
	if dropped > 0 {
		s.logger.Debug("표현할 수 없는 Workspace 항목을 목록에서 제외",
			"lab_instance_id", target.LabInstanceID.String(), "request_id", in.RequestID, "dropped", dropped)
	}
	slices.SortFunc(items, func(a, b Item) int { return strings.Compare(a.Name, b.Name) })
	return Listing{Path: dir.String(), Items: items}, nil
}

// ReadInput은 Read의 입력이다. 모든 값은 검증되지 않았다.
type ReadInput struct {
	LabInstanceID string
	Path          string
	RequestID     string
}

// File은 Read의 결과다. Content는 유효한 UTF-8이며 NUL을 포함하지 않는다.
type File struct {
	Path     string
	Content  string
	Revision Revision
}

// Read는 현재 사용자의 Workspace에서 UTF-8 text 파일 하나를 반환한다.
func (s *Service) Read(ctx context.Context, user repository.User, in ReadInput) (File, error) {
	file, err := ParseFile(in.Path)
	if err != nil {
		return File{}, err
	}
	target, err := s.resolve(ctx, user, in.LabInstanceID, in.RequestID)
	if err != nil {
		return File{}, err
	}

	data, err := s.transport.Read(ctx, target, file, s.maxBytes)
	if err != nil {
		return File{}, err
	}
	// Connector의 크기 판정을 신뢰하지 않는다. 한도를 넘는 본문은 응답하지 않는다.
	if int64(len(data.Content)) > s.maxBytes {
		return File{}, ErrTooLarge
	}
	if err := checkText(data.Content); err != nil {
		return File{}, err
	}
	if _, ok := ParseRevision(string(data.Revision)); !ok {
		// Connector가 계약을 어긴 revision을 보냈다. 이후 If-Match의 근거가 될 수 없으므로 성공으로 처리하지 않는다.
		return File{}, ErrTransportUnavailable
	}
	if err := s.stillCurrent(ctx, target); err != nil {
		return File{}, err
	}
	return File{Path: file.String(), Content: string(data.Content), Revision: data.Revision}, nil
}

// SaveInput은 Save의 입력이다. 모든 값은 검증되지 않았다.
type SaveInput struct {
	LabInstanceID string
	Path          string
	// IfMatchRevision은 If-Match의 strong entity-tag에서 따옴표를 벗긴 값이다. 같은 파일을 읽을 때 받은 revision이어야 한다.
	IfMatchRevision string
	Content         string
	RequestID       string
}

// Saved는 Save의 결과다.
type Saved struct {
	Path     string
	Revision Revision
}

// Save는 현재 사용자의 Workspace에서 기존 UTF-8 text 파일 하나를 교체한다. 파일을 만들지 않는다.
//
// 본문 검증(UTF-8, NUL, 크기)과 경로 검증은 Connector를 호출하기 전에 끝낸다. revision 비교는 Connector가 쓰기 직전에 하며 다르면
// 파일을 바꾸지 않고 ErrStaleRevision이다. Save 요청을 보낸 뒤 결과를 받지 못하면 ErrSaveOutcomeUnknown이며 자동으로 다시 보내지 않는다.
func (s *Service) Save(ctx context.Context, user repository.User, in SaveInput) (Saved, error) {
	file, err := ParseFile(in.Path)
	if err != nil {
		return Saved{}, err
	}
	if int64(len(in.Content)) > s.maxBytes {
		return Saved{}, ErrTooLarge
	}
	content := []byte(in.Content)
	if err := checkText(content); err != nil {
		return Saved{}, err
	}
	expected, expectedOK := ParseRevision(in.IfMatchRevision)

	target, err := s.resolve(ctx, user, in.LabInstanceID, in.RequestID)
	if err != nil {
		return Saved{}, err
	}
	if !expectedOK {
		// 이 서버(Connector)가 만들 수 없는 revision이다. 어떤 파일의 현재 revision과도 같을 수 없으므로 Connector를 호출하지 않는다.
		return Saved{}, ErrStaleRevision
	}

	revision, err := s.transport.Save(ctx, target, file, expected, content)
	if err != nil {
		return Saved{}, err
	}
	if _, ok := ParseRevision(string(revision)); !ok {
		// 쓰기는 끝났을 수 있으나 새 revision을 알 수 없다. 성공으로 처리할 수 없고 저장 여부도 확인할 수 없다.
		return Saved{}, ErrSaveOutcomeUnknown
	}
	if err := s.stillCurrent(ctx, target); err != nil {
		return Saved{}, err
	}
	return Saved{Path: file.String(), Revision: revision}, nil
}

// checkText는 본문이 유효한 UTF-8 text인지 확인한다. NUL을 포함하면 binary로 본다.
func checkText(content []byte) error {
	if !utf8.Valid(content) {
		return ErrUnsupportedEncoding
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return ErrBinaryContent
	}
	return nil
}

// parseID는 이 서버가 발급하는 canonical UUID 문자열만 받아들인다. 하나의 리소스가 여러 ID 표기로 보이지 않게 한다.
func parseID(raw string) (uuid.UUID, bool) {
	id, err := uuid.Parse(raw)
	if err != nil || id.String() != raw {
		return uuid.UUID{}, false
	}
	return id, true
}

// resolve는 하나의 짧은 transaction에서 현재 DB 상태로 대상 Workspace VM을 결정한다. side effect는 없고 외부 I/O도 없다.
// LabInstance를 FOR SHARE로 잠가 읽는 동안 generation이 바뀌지 않게 하며 commit 뒤에는 잠금을 잡지 않는다.
func (s *Service) resolve(ctx context.Context, user repository.User, labInstanceID, requestID string) (Target, error) {
	labID, ok := parseID(labInstanceID)
	if !ok {
		return Target{}, ErrNotFound
	}

	var target Target
	err := s.store.WithinTransaction(ctx, func(ctx context.Context, repos repository.Repositories) error {
		lab, err := repos.LabInstanceForShare(ctx, labID)
		if errors.Is(err, repository.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("workspacefile: LabInstance 조회: %w", err)
		}
		// 같은 Organization이고 LabInstance.user_id가 현재 사용자다. 강사나 Organization ADMIN도 다른 사용자의 Workspace를 열 수 없다.
		if lab.OrganizationID != user.OrganizationID || lab.UserID != user.ID {
			return ErrForbidden
		}

		membership, err := repos.ClassMembership(ctx, lab.ClassID, user.ID)
		if errors.Is(err, repository.ErrNotFound) {
			return ErrForbidden
		}
		if err != nil {
			return fmt.Errorf("workspacefile: Membership 조회: %w", err)
		}
		if membership.ClassID != lab.ClassID || membership.UserID != user.ID ||
			membership.OrganizationID != lab.OrganizationID || !membership.Role.Valid() {
			return ErrInconsistentData
		}

		if lab.Status != LabInstanceStatusReady {
			return ErrLabInstanceNotReady
		}
		if lab.Generation < 1 {
			return fmt.Errorf("%w: generation이 1 미만", ErrInconsistentData)
		}

		// Browser가 아니라 immutable CreationSnapshot이 Workspace VM을 정한다. 최신 LabSpec을 다시 해석하지 않는다.
		snapshot, err := repos.CreationSnapshotTargets(ctx, lab.ID)
		if errors.Is(err, repository.ErrNotFound) {
			return fmt.Errorf("%w: CreationSnapshot 없음", ErrInconsistentData)
		}
		if err != nil {
			return fmt.Errorf("workspacefile: CreationSnapshot 조회: %w", err)
		}
		vmKey, err := workspaceVMKey(snapshot)
		if err != nil {
			return err
		}

		connectorID, err := repos.ConnectorIDForLabInstance(ctx, lab.ID)
		if errors.Is(err, repository.ErrNotFound) {
			// CreationSnapshot → ProviderConnection → Connector 관계는 DB 제약상 있어야 한다.
			return fmt.Errorf("%w: Connector 관계 없음", ErrInconsistentData)
		}
		if err != nil {
			return fmt.Errorf("workspacefile: Connector 조회: %w", err)
		}

		servers, err := repos.ProviderServers(ctx, lab.ID, lab.Generation, vmKey)
		if err != nil {
			return fmt.Errorf("workspacefile: Workspace VM 조회: %w", err)
		}
		var present []repository.ProviderServer
		for _, server := range servers {
			if server.LifecycleStatus == ProviderResourcePresent {
				present = append(present, server)
			}
		}
		switch len(present) {
		case 0:
			// snapshot에는 있는 VM인데 현재 generation에 PRESENT SERVER ProviderResource가 없다(Reset 진행·Provider drift 등).
			// Browser가 잘못 보낸 값이 아니라 지금 사용할 수 없는 것이다.
			return ErrTargetUnavailable
		case 1:
		default:
			// 같은 generation에 같은 논리 이름의 PRESENT VM이 둘 이상이면 어느 쪽인지 알 수 없다. 임의로 고르지 않는다.
			return fmt.Errorf("%w: PRESENT Workspace VM이 둘 이상", ErrInconsistentData)
		}
		if present[0].ProviderID == "" {
			return fmt.Errorf("%w: Workspace VM의 Provider ID가 비어 있음", ErrInconsistentData)
		}

		target = Target{
			LabInstanceID:    lab.ID,
			Generation:       lab.Generation,
			ConnectorID:      connectorID,
			WorkspaceVMKey:   vmKey,
			ProviderServerID: present[0].ProviderID,
			RequestID:        requestID,
		}
		return nil
	})
	if err != nil {
		return Target{}, err
	}
	return target, nil
}

// workspaceVMKey는 CreationSnapshot에서 Workspace VM key를 읽는다. 저장된 값을 신뢰하지 않는다.
// connector.schema.json의 CreationSnapshot처럼 workspaceVmKey는 비어 있지 않고 vms 중 정확히 하나를 가리켜야 한다.
// 해석할 수 없거나 모순이면 값을 만들어 채우지 않고 ErrInconsistentData로 fail closed한다. 오류 문구에는 값을 넣지 않는다.
func workspaceVMKey(snapshot repository.CreationSnapshotTargets) (string, error) {
	if snapshot.WorkspaceVMKey == "" {
		return "", fmt.Errorf("%w: CreationSnapshot에 workspaceVmKey가 없음", ErrInconsistentData)
	}
	matches := 0
	for _, vm := range snapshot.VMs {
		if vm.VMKey == snapshot.WorkspaceVMKey {
			matches++
		}
	}
	switch matches {
	case 0:
		return "", fmt.Errorf("%w: workspaceVmKey가 vms에 없음", ErrInconsistentData)
	case 1:
		return snapshot.WorkspaceVMKey, nil
	default:
		return "", fmt.Errorf("%w: workspaceVmKey가 vms에 중복됨", ErrInconsistentData)
	}
}

// stillCurrent는 외부 I/O가 끝난 뒤 target을 결정한 시점의 LabInstance가 그대로인지 확인한다. Reset이 그 사이에 generation을 바꿨거나
// LabInstance가 READY를 벗어났다면 결과를 성공으로 처리하지 않는다. 짧은 단일 조회이며 분산 lock을 쓰지 않는다.
func (s *Service) stillCurrent(ctx context.Context, target Target) error {
	lab, err := s.store.LabInstanceByID(ctx, target.LabInstanceID)
	if errors.Is(err, repository.ErrNotFound) {
		return ErrTargetChanged
	}
	if err != nil {
		return fmt.Errorf("workspacefile: LabInstance 재확인: %w", err)
	}
	if lab.Generation != target.Generation || lab.Status != LabInstanceStatusReady {
		s.logger.Warn("Workspace file 처리 중 대상이 바뀜",
			"lab_instance_id", target.LabInstanceID.String(),
			"request_id", target.RequestID,
			"generation", target.Generation,
			"error_code", "WORKSPACE_TARGET_CHANGED",
		)
		return ErrTargetChanged
	}
	return nil
}
