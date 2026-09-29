package postgres

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Migration은 db/migrations의 SQL 파일 하나다.
type Migration struct {
	Version int64
	Name    string
	// Checksum은 파일 전체의 SHA-256이다. 이미 적용된 파일이 바뀌었는지 판단한다.
	Checksum string
	// body는 최상위 BEGIN;/COMMIT; envelope을 제거한 SQL이다.
	body string
}

var (
	migrationFilePattern = regexp.MustCompile(`^([0-9]+)_[a-z0-9_]+\.sql$`)
	// 흔한 transaction 제어문을 파일 적재 시점에 일찍 거부하기 위한 defense-in-depth다.
	// SQL을 해석하지 않으므로 한 줄의 여러 statement나 END; 같은 형태는 놓칠 수 있으며,
	// 정합성은 applyMigration의 transaction identity·commit guard·read-only 기본값이 보장한다.
	// PL/pgSQL block의 BEGIN/END;와 구분하기 위해 한 줄 전체가 제어문인 경우만 찾는다.
	transactionControlPattern = regexp.MustCompile(`(?i)^(BEGIN|START\s+TRANSACTION|COMMIT|ROLLBACK|ABORT|END\s+(TRANSACTION|WORK))\b[^;]*;$`)
)

// LoadMigrations는 fsys 최상위의 *.sql 파일을 version 순서로 읽는다.
func LoadMigrations(fsys fs.FS) ([]Migration, error) {
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("migration 파일 목록 조회 실패: %w", err)
	}
	if len(names) == 0 {
		return nil, errors.New("migration 파일이 없습니다")
	}

	migrations := make([]Migration, 0, len(names))
	seen := make(map[int64]string, len(names))
	for _, name := range names {
		match := migrationFilePattern.FindStringSubmatch(name)
		if match == nil {
			return nil, fmt.Errorf("migration 파일 이름 형식 오류: %s", name)
		}
		version, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("migration version 형식 오류: %s", name)
		}
		if previous, ok := seen[version]; ok {
			return nil, fmt.Errorf("migration version이 중복되었습니다: %s, %s", previous, name)
		}
		seen[version] = name

		content, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("migration 파일 읽기 실패: %s: %w", name, err)
		}
		body, err := transactionBody(string(content))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		sum := sha256.Sum256(content)
		migrations = append(migrations, Migration{
			Version:  version,
			Name:     name,
			Checksum: hex.EncodeToString(sum[:]),
			body:     body,
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})
	return migrations, nil
}

// transactionBody는 파일 최상위 BEGIN;/COMMIT; envelope을 제거한 본문을 반환한다.
// runner는 적용 기록을 같은 transaction에 남기기 위해 envelope을 직접 소유한다.
func transactionBody(content string) (string, error) {
	lines := strings.Split(content, "\n")
	first, last := -1, -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
	}

	if first < 0 || first == last ||
		!strings.EqualFold(strings.TrimSpace(lines[first]), "BEGIN;") ||
		!strings.EqualFold(strings.TrimSpace(lines[last]), "COMMIT;") {
		return "", errors.New("migration은 최상위 BEGIN; ... COMMIT; 하나로 감싸야 합니다")
	}

	inner := lines[first+1 : last]
	for _, line := range inner {
		if transactionControlPattern.MatchString(strings.TrimSpace(line)) {
			return "", errors.New("migration envelope 안에 transaction 제어문을 둘 수 없습니다")
		}
	}
	return strings.Join(inner, "\n"), nil
}
