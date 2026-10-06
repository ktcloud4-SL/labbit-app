package filetransport

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Connector File transport adapter는 PostgreSQL, repository, Browser HTTP를 알지 못한다. 권한과 대상 결정은 workspacefile이 하고
// 이 package는 결정된 Target으로 Connector와 통신만 한다.
func TestFileTransportDoesNotImportDatabaseOrHTTPLayers(t *testing.T) {
	forbidden := []string{
		"database/sql",
		"github.com/jackc/pgx",
		"github.com/ktcloud4-SL/labbit-app/internal/postgres",
		"github.com/ktcloud4-SL/labbit-app/internal/server/repository",
		"github.com/ktcloud4-SL/labbit-app/internal/server/httpapi",
		"github.com/ktcloud4-SL/labbit-app/internal/server/auth",
		"github.com/ktcloud4-SL/labbit-app/internal/server/class",
		"github.com/ktcloud4-SL/labbit-app/internal/server/terminal",
		"github.com/ktcloud4-SL/labbit-app/internal/server/realtime",
	}
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
			for _, prefix := range forbidden {
				if path == prefix || strings.HasPrefix(path, prefix+"/") {
					t.Errorf("%s가 %s를 import합니다", file, path)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("검사할 Go 파일을 찾지 못했습니다")
	}
}
