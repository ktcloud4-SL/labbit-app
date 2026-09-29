package repository_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Application이 SQL/pgx를 알 필요가 없다는 경계를 지킨다.
// port(repository)와 그 위의 use case(bootstrap)는 pgx와 PostgreSQL adapter를 import하지 않는다.
func TestApplicationPackagesDoNotImportPostgres(t *testing.T) {
	forbidden := []string{
		"github.com/jackc/pgx",
		"github.com/ktcloud4-SL/labbit-app/internal/postgres",
		"database/sql",
	}

	for _, dir := range []string{".", "../bootstrap"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
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
			t.Fatalf("%s에서 검사할 Go 파일을 찾지 못했습니다", dir)
		}
	}
	if _, err := os.Stat("../bootstrap"); err != nil {
		t.Fatal(err)
	}
}
