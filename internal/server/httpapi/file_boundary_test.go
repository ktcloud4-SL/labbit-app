package httpapi

import (
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// Workspace file handler는 pgx/SQL과 Connector protocol, File transport 세부를 알지 못한다. Application(workspacefile)에만 위임한다.
func TestFileHandlerDoesNotImportPersistenceOrConnectorProtocol(t *testing.T) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "file.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		"database/sql",
		"github.com/gorilla/websocket",
		"github.com/jackc/pgx",
		"github.com/ktcloud4-SL/labbit-app/internal/postgres",
		"github.com/ktcloud4-SL/labbit-app/internal/connector",
		"github.com/ktcloud4-SL/labbit-app/internal/server/connector",
		"github.com/ktcloud4-SL/labbit-app/internal/server/connectorwss",
		"github.com/ktcloud4-SL/labbit-app/internal/server/filetransport",
	}
	for _, spec := range parsed.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		for _, prefix := range forbidden {
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				t.Errorf("file.go가 %s를 import합니다", path)
			}
		}
	}
}
