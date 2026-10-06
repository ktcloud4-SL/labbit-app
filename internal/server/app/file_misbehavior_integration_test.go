//go:build integration

package app

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/server/filetransport/filetest"
)

// 계약을 어기는 Connector의 응답은 성공으로 처리하지 않는다. 잘못된 correlation, 시간 초과는 HTTP에서 503이고 Save는 저장 여부를 알 수 없다고 알린다.
// database 생성이 느리므로 환경 하나에서 Connector의 동작만 바꿔 가며 확인한다.
func TestWorkspaceFileConnectorMisbehavior(t *testing.T) {
	e := newFileEnv(t, []string{"file-v1"}, func(o *stackOptions) {
		o.FileAttachTimeout = 300 * time.Millisecond
		o.FileOperationTimeout = 300 * time.Millisecond
	})
	e.seed()
	lab := e.fixture.LabInstanceID.String()
	etag := `"` + e.fs.RevisionOf("main.py") + `"`

	t.Run("다른 generation으로 attach", func(t *testing.T) {
		e.setBehavior(func(filetest.Open) filetest.Behavior {
			return filetest.Behavior{Attach: func(f filetest.Frame) { f["generation"] = 99 }}
		})
		if code := e.problemCode(e.get(e.contentPath(lab, "main.py"), e.ownerCookie), http.StatusServiceUnavailable); code != "file_transport_unavailable" {
			t.Fatalf("code = %q", code)
		}
	})

	t.Run("다른 LabInstance로 attach", func(t *testing.T) {
		e.setBehavior(func(filetest.Open) filetest.Behavior {
			return filetest.Behavior{Attach: func(f filetest.Frame) { f["labInstanceId"] = e.fixture.PeerLabInstanceID.String() }}
		})
		if code := e.problemCode(e.get(e.treePath(lab, ""), e.ownerCookie), http.StatusServiceUnavailable); code != "file_transport_unavailable" {
			t.Fatalf("code = %q", code)
		}
	})

	t.Run("잘못된 요청 ID의 결과", func(t *testing.T) {
		e.setBehavior(func(filetest.Open) filetest.Behavior {
			return filetest.Behavior{Result: func(f filetest.Frame) { f["replyToMessageId"] = "another-request" }}
		})
		if code := e.problemCode(e.get(e.contentPath(lab, "main.py"), e.ownerCookie), http.StatusServiceUnavailable); code != "file_transport_unavailable" {
			t.Fatalf("code = %q", code)
		}
		// Save는 본문을 보낸 뒤이므로 성공도 실패도 확정할 수 없다. 자동으로 다시 보내지 않는다.
		before := len(e.peer.Opens())
		if code := e.problemCode(e.put(e.contentPath(lab, "main.py"), e.ownerCookie, etag, "changed"), http.StatusServiceUnavailable); code != "file_save_outcome_unknown" {
			t.Fatalf("Save code = %q", code)
		}
		if got := len(e.peer.Opens()) - before; got != 1 {
			t.Fatalf("Save FILE_OPEN %d개, want 1(자동 재전송 금지)", got)
		}
	})

	t.Run("결과가 시간 안에 오지 않음", func(t *testing.T) {
		stall := make(chan struct{})
		t.Cleanup(func() { close(stall) })
		e.setBehavior(func(filetest.Open) filetest.Behavior { return filetest.Behavior{Stall: stall} })
		if code := e.problemCode(e.get(e.treePath(lab, ""), e.ownerCookie), http.StatusServiceUnavailable); code != "file_transport_unavailable" {
			t.Fatalf("code = %q", code)
		}
		if code := e.problemCode(e.put(e.contentPath(lab, "main.py"), e.ownerCookie, etag, "changed2"), http.StatusServiceUnavailable); code != "file_save_outcome_unknown" {
			t.Fatalf("Save code = %q", code)
		}
		eventually(t, "FILE_CLOSE", 5*time.Second, func() bool {
			for _, c := range e.peer.Closes() {
				if c.Reason == "REQUEST_TIMEOUT" {
					return true
				}
			}
			return false
		})
	})

	t.Run("Connector가 VM 파일 권한 거절을 보고", func(t *testing.T) {
		e.setBehavior(func(filetest.Open) filetest.Behavior {
			return filetest.Behavior{Result: func(f filetest.Frame) {
				f["payload"] = map[string]any{"outcome": "FAILED", "error": map[string]any{"code": "PERMISSION_DENIED", "message": "sftp: " + fileSourceMarker}}
			}}
		})
		resp := e.get(e.contentPath(lab, "main.py"), e.ownerCookie)
		if code := e.problemCode(resp, http.StatusForbidden); code != "file_permission_denied" {
			t.Fatalf("code = %q", code)
		}
		// Connector가 보낸 문구는 응답이나 log에 복사하지 않는다.
		if strings.Contains(string(resp.Body), fileSourceMarker) || strings.Contains(e.logs.String(), fileSourceMarker) {
			t.Fatal("Connector가 보낸 문구가 응답이나 log에 남음")
		}
	})

	eventually(t, "pending cleanup", 5*time.Second, func() bool {
		return e.stack.FileBroker.PendingCount() == 0 && e.peer.Active() == 0
	})
}
