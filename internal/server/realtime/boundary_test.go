package realtime_test

import (
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Runtime Contract의 realtime role은 PostgreSQL을 application dependency로 요구하지 않는다. 그래서 이 package는 저장소 구현,
// repository port, DB-backed use case를 import하지 않고 자신이 정의한 좁은 interface(Control, ConnectorAuthenticator)로만 authority를 쓴다.
var forbiddenImports = []string{
	"github.com/jackc/pgx",
	"github.com/ktcloud4-SL/labbit-app/internal/postgres",
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository",
	"github.com/ktcloud4-SL/labbit-app/internal/server/auth",
	"github.com/ktcloud4-SL/labbit-app/internal/server/class",
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector",
	"github.com/ktcloud4-SL/labbit-app/internal/server/connectorwss",
	"github.com/ktcloud4-SL/labbit-app/internal/server/httpapi",
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal",
	"github.com/ktcloud4-SL/labbit-app/internal/server/bootstrap",
}

func isForbidden(path string) bool {
	// database/sql 자체는 금지하지만 database/sql/driver는 driver 작성자용 interface만 담은 표준 package이며
	// github.com/google/uuid가 Scanner/Valuer 구현 때문에 가져온다. DB 접근 계층이 아니므로 허용한다.
	if path == "database/sql" {
		return true
	}
	for _, prefix := range forbiddenImports {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// 직접 import를 검사한다. test 파일은 fake를 쓰므로 production 파일만 본다.
func TestRealtimePackageDoesNotImportDatabasePackages(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s 해석 실패: %v", file, err)
		}
		checked++
		for _, spec := range parsed.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if isForbidden(path) {
				t.Errorf("%s가 %s를 import합니다", file, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("검사할 Go 파일을 찾지 못했습니다")
	}
}

// 전이 의존성도 검사한다. 허용된 import가 간접적으로 PostgreSQL을 끌어오지 않는다.
func TestRealtimePackageHasNoTransitiveDatabaseDependency(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps 실패: %v", err)
	}
	deps := strings.Fields(string(out))
	if len(deps) == 0 {
		t.Fatal("의존성 목록이 비어 있음")
	}
	for _, dep := range deps {
		if isForbidden(dep) {
			t.Errorf("realtime이 전이적으로 %s에 의존합니다", dep)
		}
	}
}
