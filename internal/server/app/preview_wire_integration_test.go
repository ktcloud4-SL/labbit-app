//go:build integration

package app

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/server/preview/previewtest"
)

// TestDumpPreviewWireSamples는 실제 실행에서 오간 Connector wire frame을 LABBIT_WIRE_SAMPLE_DIR에 저장한다. 환경변수가 없으면 건너뛴다.
// 저장한 frame을 contracts/connector의 JSON Schema로 검증하는 것은 CI의 일반 test가 아니라 producer/consumer 교차 확인이다.
//
//	LABBIT_WIRE_SAMPLE_DIR=/tmp/samples go test -tags integration ./internal/server/app -run TestDumpPreviewWireSamples
func TestDumpPreviewWireSamples(t *testing.T) {
	dir := os.Getenv("LABBIT_WIRE_SAMPLE_DIR")
	if dir == "" {
		t.Skip("LABBIT_WIRE_SAMPLE_DIR가 없어 wire sample을 저장하지 않음")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})

	// 정상 생성과 종료: HELLO(capabilities), PREVIEW_OPEN, PREVIEW_OPEN_RESULT SUCCEEDED, PREVIEW_ATTACH, PREVIEW_ATTACHED, PREVIEW_CLOSE.
	s := e.mustCreate(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	cookie := e.login(s)
	if _, body := e.get(s, cookie, "/"); body == "" {
		t.Fatal("Preview 응답이 비어 있음")
	}
	if resp := e.request(http.MethodDelete, "/api/v1/preview-sessions/"+s.ID, e.ownerCookie, "", nil); resp.Status != http.StatusNoContent {
		t.Fatalf("DELETE status = %d", resp.Status)
	}
	eventually(t, "PREVIEW_CLOSE", 5*time.Second, func() bool { return len(e.peer.Closes()) == 1 })

	// Connector의 실패 보고: PREVIEW_OPEN_RESULT FAILED(error.code)와 error 없는 FAILED.
	for _, code := range []string{"APP_NOT_RUNNING", "PORT_REJECTED", "VM_UNREACHABLE", ""} {
		e.setBehavior(func(previewtest.Open) previewtest.Behavior { return previewtest.Behavior{FailOpen: code} })
		if code == "" {
			continue
		}
		e.create(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	}

	var n int
	save := func(kind string, frames [][]byte) {
		for _, raw := range frames {
			n++
			name := filepath.Join(dir, kind+"-"+strconv.Itoa(n)+".json")
			if err := os.WriteFile(name, raw, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	save("saas-to-connector-control", e.peer.ControlFrames())
	save("connector-to-saas-control", e.peer.SentControlFrames())
	var data [][]byte
	for _, f := range e.peer.DataFrames() {
		if !f.Binary {
			data = append(data, f.Raw)
		}
	}
	save("data", data)
}
