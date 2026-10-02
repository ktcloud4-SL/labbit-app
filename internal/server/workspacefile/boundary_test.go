package workspacefile_test

import (
	"os/exec"
	"strings"
	"testing"
)

// Application은 HTTP, PostgreSQL, Connector protocol, WebSocket을 알지 못한다. 파일 I/O는 Transport port로만 하고 영속 상태는
// repository port로만 읽는다. 직접 import뿐 아니라 전이 의존까지 검사한다(go list -deps).
func TestWorkspaceFileApplicationDoesNotDependOnTransportsOrDatabaseAdapters(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps 실패: %v", err)
	}
	forbidden := []string{
		"net/http",
		"database/sql",
		"github.com/gorilla/websocket",
		"github.com/jackc/pgx",
		"github.com/ktcloud4-SL/labbit-app/internal/postgres",
		"github.com/ktcloud4-SL/labbit-app/internal/connector",
		"github.com/ktcloud4-SL/labbit-app/internal/server/connector",
		"github.com/ktcloud4-SL/labbit-app/internal/server/connectorwss",
		"github.com/ktcloud4-SL/labbit-app/internal/server/filetransport",
		"github.com/ktcloud4-SL/labbit-app/internal/server/httpapi",
		"github.com/ktcloud4-SL/labbit-app/internal/server/realtime",
		"github.com/ktcloud4-SL/labbit-app/internal/server/terminal",
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, prefix := range forbidden {
			// database/sql/driver는 github.com/google/uuid가 Scanner/Valuer 구현 때문에 가져오는 표준 package이며 DB 접근 계층이 아니다.
			if prefix == "database/sql" && dep != prefix {
				continue
			}
			if dep == prefix || strings.HasPrefix(dep, prefix+"/") {
				t.Errorf("workspacefile이 %s에 의존합니다", dep)
			}
		}
	}
}
