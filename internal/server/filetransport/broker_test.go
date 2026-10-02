package filetransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/filetransport/filetest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/workspacefile"
)

func sampleFS() *filetest.FS {
	fs := filetest.NewFS()
	fs.Put("main.py", []byte("print('hello')\n"))
	fs.Put("src/app.py", []byte("def main():\n    pass\n"))
	fs.Put("src/한글.txt", []byte("안녕\n"))
	fs.Put("empty.txt", nil)
	fs.Mkdir("docs")
	return fs
}

func TestTreeReadSaveRoundTripThroughTheContractPeer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fs := sampleFS()
	p := h.fileV1Peer(fs, nil)
	ctx := context.Background()

	// Tree: root와 하위 디렉터리, 비어 있는 디렉터리.
	root, err := h.tree(ctx, "")
	if err != nil {
		t.Fatalf("Tree(root) error = %v", err)
	}
	wantRoot := []workspacefile.Entry{
		{Name: "docs", Kind: workspacefile.KindDirectory},
		{Name: "empty.txt", Kind: workspacefile.KindFile},
		{Name: "main.py", Kind: workspacefile.KindFile},
		{Name: "src", Kind: workspacefile.KindDirectory},
	}
	if len(root) != len(wantRoot) {
		t.Fatalf("Tree(root) = %+v, want %+v", root, wantRoot)
	}
	for i := range wantRoot {
		if root[i] != wantRoot[i] {
			t.Fatalf("Tree(root)[%d] = %+v, want %+v", i, root[i], wantRoot[i])
		}
	}
	src, err := h.tree(ctx, "src")
	if err != nil || len(src) != 2 {
		t.Fatalf("Tree(src) = %+v, %v", src, err)
	}
	if docs, err := h.tree(ctx, "docs"); err != nil || len(docs) != 0 {
		t.Fatalf("Tree(docs) = %+v, %v, want an empty directory", docs, err)
	}

	// Read: 본문과 revision.
	data, err := h.read(ctx, "main.py", 1024)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if string(data.Content) != "print('hello')\n" || string(data.Revision) != fs.RevisionOf("main.py") {
		t.Fatalf("Read() = %q rev %q", data.Content, data.Revision)
	}
	if uni, err := h.read(ctx, "src/한글.txt", 1024); err != nil || string(uni.Content) != "안녕\n" {
		t.Fatalf("Read(unicode) = %q, %v", uni.Content, err)
	}
	if empty, err := h.read(ctx, "empty.txt", 1024); err != nil || len(empty.Content) != 0 || empty.Revision == "" {
		t.Fatalf("Read(empty) = %+v, %v", empty, err)
	}

	// Save: 읽은 revision으로 저장하면 새 revision을 돌려주고 본문이 바뀐다.
	revision, err := h.save(ctx, "main.py", string(data.Revision), "print('changed')\n")
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if got, _ := fs.Content("main.py"); string(got) != "print('changed')\n" {
		t.Fatalf("저장된 본문 = %q", got)
	}
	if string(revision) != fs.RevisionOf("main.py") || revision == data.Revision {
		t.Fatalf("Save() revision = %q (이전 %q)", revision, data.Revision)
	}

	// 같은 revision으로 다시 저장하면 stale이다. 파일을 덮어쓰지 않는다.
	if _, err := h.save(ctx, "main.py", string(data.Revision), "print('overwrite')\n"); !errors.Is(err, workspacefile.ErrStaleRevision) {
		t.Fatalf("stale Save() error = %v, want ErrStaleRevision", err)
	}
	if got, _ := fs.Content("main.py"); string(got) != "print('changed')\n" {
		t.Fatalf("stale Save()가 파일을 바꿈: %q", got)
	}
	// 새 revision으로는 저장된다(빈 본문 포함).
	if _, err := h.save(ctx, "main.py", string(revision), ""); err != nil {
		t.Fatalf("Save(empty) error = %v", err)
	}
	if got, ok := fs.Content("main.py"); !ok || len(got) != 0 {
		t.Fatalf("빈 본문 저장 결과 = %q, %v", got, ok)
	}

	// 모든 요청은 요청별 Data WSS 하나와 FILE_OPEN 하나다.
	if got := len(p.Opens()); got != 9 {
		t.Fatalf("FILE_OPEN %d개, want 9 (요청마다 하나)", got)
	}
	h.waitIdle(p)
}

// Control에는 lifecycle/correlation metadata만 싣고 경로, 디렉터리 목록, 본문은 File Data WSS로만 전달한다.
// log에도 경로와 본문이 남지 않는다.
func TestControlCarriesLifecycleMetadataOnlyAndNothingLeaksIntoLogs(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fs := filetest.NewFS()
	secretFile := secretDir + "/" + sourceMarker + ".txt"
	fs.Put(secretFile, []byte("body "+sourceMarker+"\n"))
	p := h.fileV1Peer(fs, nil)
	ctx := context.Background()

	if _, err := h.tree(ctx, secretDir); err != nil {
		t.Fatalf("Tree() error = %v", err)
	}
	data, err := h.read(ctx, secretFile, 1024)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if _, err := h.save(ctx, secretFile, string(data.Revision), "new "+sourceMarker); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := h.read(ctx, secretDir+"/ghost", 1024); !errors.Is(err, workspacefile.ErrPathNotFound) {
		t.Fatalf("Read(ghost) error = %v", err)
	}
	h.waitIdle(p)

	// 양성 대조: Data WSS에는 경로와 본문이 실제로 흐른다. 아래 검사가 의미 있는 검사임을 보장한다.
	var onData bytes.Buffer
	for _, f := range p.DataFrames() {
		onData.Write(f.Raw)
	}
	if !strings.Contains(onData.String(), secretFile) || !strings.Contains(onData.String(), sourceMarker) {
		t.Fatal("Data WSS에 경로나 본문이 흐르지 않음(검사가 무의미)")
	}

	// Control frame은 lifecycle/correlation metadata뿐이다.
	frames := p.ControlFrames()
	var opens, closes int
	for _, raw := range frames {
		s := string(raw)
		if strings.Contains(s, sourceMarker) || strings.Contains(s, secretDir) || strings.Contains(s, "content") {
			t.Fatalf("Control frame에 경로나 본문이 있음: %s", s)
		}
		var msg struct {
			Type    string         `json:"type"`
			Payload map[string]any `json:"payload"`
		}
		if json.Unmarshal(raw, &msg) != nil {
			t.Fatalf("Control frame이 JSON이 아님: %s", s)
		}
		switch msg.Type {
		case "FILE_OPEN":
			opens++
			if len(msg.Payload) != 3 || msg.Payload["operation"] == nil || msg.Payload["targetVmKey"] != "vk-web" || msg.Payload["providerServerId"] != "srv-web-g3" {
				t.Fatalf("FILE_OPEN payload = %v", msg.Payload)
			}
		case "FILE_CLOSE":
			closes++
		case "HELLO_ACK":
		default:
			t.Fatalf("예상하지 못한 Control frame: %s", s)
		}
	}
	if opens != 4 || closes != 0 {
		t.Fatalf("FILE_OPEN %d개 FILE_CLOSE %d개, want 4/0", opens, closes)
	}

	// 구조화 log: 요청을 상관시킬 수 있는 metadata만 남기고 경로, 본문, Credential은 남기지 않는다.
	logs := h.logs.String()
	for _, secret := range []string{sourceMarker, secretDir, "new " + sourceMarker, credentialA, credentialB} {
		if strings.Contains(logs, secret) {
			t.Fatalf("log에 민감한 값이 남음: %q\n%s", secret, logs)
		}
	}
	if !strings.Contains(logs, "file_request_id") || !strings.Contains(logs, "lab_instance_id") || !strings.Contains(logs, "connector_id") {
		t.Fatalf("log에 correlation 정보가 없음:\n%s", logs)
	}
}

// 오류 문구에도 경로와 본문이 들어가지 않는다.
func TestErrorsNeverCarryPathsOrContent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fs := filetest.NewFS()
	fs.Put(secretDir+"/a.txt", []byte(sourceMarker))
	h.fileV1Peer(fs, func(filetest.Open) filetest.Behavior {
		return filetest.Behavior{Result: func(f filetest.Frame) {
			f["payload"] = map[string]any{"outcome": "FAILED", "error": map[string]any{"code": "UNAVAILABLE", "message": "sftp: " + sourceMarker + " " + secretDir}}
		}}
	})
	_, err := h.read(context.Background(), secretDir+"/a.txt", 1024)
	if !errors.Is(err, workspacefile.ErrTransportUnavailable) {
		t.Fatalf("Read() error = %v", err)
	}
	if strings.Contains(err.Error(), sourceMarker) || strings.Contains(err.Error(), secretDir) {
		t.Fatalf("오류 문구에 Connector가 보낸 문구가 있음: %q", err)
	}
	if logs := h.logs.String(); strings.Contains(logs, sourceMarker) || strings.Contains(logs, secretDir) {
		t.Fatalf("log에 Connector가 보낸 문구가 있음:\n%s", logs)
	}
}

// file-v1을 선언하지 않은 Connector에는 FILE_OPEN을 보내지 않고 그 요청은 사용 불가다. 버전으로 추론하지 않는다.
func TestConnectorWithoutFileV1IsNeverAsked(t *testing.T) {
	t.Parallel()
	for name, capabilities := range map[string][]string{
		"capabilities field 없음": nil,
		"빈 capabilities":        {},
		"다른 capability":         {"terminal", "preview"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			p := h.peer(capabilities, sampleFS(), nil)

			_, err := h.tree(context.Background(), "")
			if !errors.Is(err, workspacefile.ErrTransportUnavailable) {
				t.Fatalf("Tree() error = %v, want ErrTransportUnavailable", err)
			}
			if errors.Is(err, workspacefile.ErrConnectorUnavailable) {
				t.Fatal("capability 미선언을 Connector 단절로 보고함")
			}
			if _, err := h.read(context.Background(), "main.py", 10); !errors.Is(err, workspacefile.ErrTransportUnavailable) {
				t.Fatalf("Read() error = %v", err)
			}
			if _, err := h.save(context.Background(), "main.py", "r1", "x"); !errors.Is(err, workspacefile.ErrTransportUnavailable) {
				t.Fatalf("Save() error = %v", err)
			}
			// 어떤 FILE Control message도 받지 않았다.
			if len(p.Opens()) != 0 || len(p.Closes()) != 0 {
				t.Fatalf("capability 없는 Connector가 File message를 받음: opens=%v closes=%v", p.Opens(), p.Closes())
			}
			for _, raw := range p.ControlFrames() {
				if strings.Contains(string(raw), "FILE_") {
					t.Fatalf("capability 없는 Connector가 File message를 받음: %s", raw)
				}
			}
			h.waitIdle(p)
		})
	}
}

func TestNoConnectorIsUnavailable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if _, err := h.tree(context.Background(), ""); !errors.Is(err, workspacefile.ErrConnectorUnavailable) {
		t.Fatalf("Tree() error = %v, want ErrConnectorUnavailable", err)
	}
	if _, err := h.save(context.Background(), "a", "r", "x"); !errors.Is(err, workspacefile.ErrConnectorUnavailable) {
		t.Fatalf("Save() error = %v, want ErrConnectorUnavailable", err)
	}
	h.waitIdle(nil)
}

// Connector가 보고한 결과가 application error로 바뀐다. 알 수 없는 code는 사용 불가다.
func TestConnectorReportedFailuresMapToApplicationErrors(t *testing.T) {
	t.Parallel()
	fs := sampleFS()
	fs.Put("big.txt", bytes.Repeat([]byte("x"), 64))
	h := newHarness(t)
	h.fileV1Peer(fs, nil)
	ctx := context.Background()

	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"Tree: 없는 디렉터리", func() error { _, err := h.tree(ctx, "ghost"); return err }, workspacefile.ErrPathNotFound},
		{"Tree: 파일", func() error { _, err := h.tree(ctx, "main.py"); return err }, workspacefile.ErrNotADirectory},
		{"Read: 없는 파일", func() error { _, err := h.read(ctx, "ghost.txt", 10); return err }, workspacefile.ErrPathNotFound},
		{"Read: 디렉터리", func() error { _, err := h.read(ctx, "src", 10); return err }, workspacefile.ErrNotAFile},
		{"Read: 한도 초과", func() error { _, err := h.read(ctx, "big.txt", 10); return err }, workspacefile.ErrTooLarge},
		{"Save: 없는 파일(만들지 않음)", func() error { _, err := h.save(ctx, "ghost.txt", "r", "x"); return err }, workspacefile.ErrPathNotFound},
		{"Save: 디렉터리", func() error { _, err := h.save(ctx, "src", "r", "x"); return err }, workspacefile.ErrNotAFile},
		{"Save: stale revision", func() error { _, err := h.save(ctx, "main.py", "stale", "x"); return err }, workspacefile.ErrStaleRevision},
	}
	for _, tc := range cases {
		if err := tc.call(); !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v, want %v", tc.name, err, tc.want)
		}
	}
	if _, ok := fs.Content("ghost.txt"); ok {
		t.Error("Save가 없는 파일을 만듦")
	}
}

func TestConnectorErrorCodesMapPerOperation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code string
		op   string
		want error
	}{
		{"NOT_FOUND", "tree", workspacefile.ErrPathNotFound},
		{"NOT_A_FILE", "read", workspacefile.ErrNotAFile},
		{"NOT_A_DIRECTORY", "tree", workspacefile.ErrNotADirectory},
		{"TOO_LARGE", "read", workspacefile.ErrTooLarge},
		{"TOO_LARGE", "tree", workspacefile.ErrTooLarge},
		{"REVISION_CONFLICT", "save", workspacefile.ErrStaleRevision},
		// Save가 아닌 작업의 revision 충돌은 계약에 없다. 알 수 없는 결과다.
		{"REVISION_CONFLICT", "read", workspacefile.ErrTransportUnavailable},
		{"PERMISSION_DENIED", "read", workspacefile.ErrFilePermissionDenied},
		{"PERMISSION_DENIED", "save", workspacefile.ErrFilePermissionDenied},
		{"INVALID_PATH", "read", workspacefile.ErrInvalidPath},
		{"UNAVAILABLE", "read", workspacefile.ErrTransportUnavailable},
		{"INTERNAL_ERROR", "tree", workspacefile.ErrTransportUnavailable},
		{"SOMETHING_NEW", "read", workspacefile.ErrTransportUnavailable},
		{"not_found", "read", workspacefile.ErrTransportUnavailable}, // code는 대소문자를 구분한다.
	}
	for _, tc := range cases {
		t.Run(tc.op+"/"+tc.code, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			code := tc.code
			h.fileV1Peer(sampleFS(), func(filetest.Open) filetest.Behavior {
				return filetest.Behavior{Result: func(f filetest.Frame) {
					f["payload"] = map[string]any{"outcome": "FAILED", "error": map[string]any{"code": code, "message": "x"}}
				}}
			})
			var err error
			switch tc.op {
			case "tree":
				_, err = h.tree(context.Background(), "")
			case "read":
				_, err = h.read(context.Background(), "main.py", 1024)
			default:
				_, err = h.save(context.Background(), "main.py", "r", "x")
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			// Connector가 FAILED를 알렸으므로 Save라도 "알 수 없음"이 아니라 확정된 실패다.
			if errors.Is(err, workspacefile.ErrSaveOutcomeUnknown) {
				t.Fatalf("Connector가 보고한 실패를 저장 여부 불명으로 처리함: %v", err)
			}
		})
	}
}

// Connector가 FILE_OPEN_RESULT=FAILED로 알리면 attach timeout을 기다리지 않고 즉시 실패한다.
func TestOpenResultFailedFailsFast(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(o *Options) { o.AttachTimeout = 5 * time.Second })
	p := h.fileV1Peer(sampleFS(), func(filetest.Open) filetest.Behavior { return filetest.Behavior{FailOpen: "UNAVAILABLE"} })

	start := time.Now()
	_, err := h.tree(context.Background(), "")
	if !errors.Is(err, workspacefile.ErrTransportUnavailable) {
		t.Fatalf("Tree() error = %v, want ErrTransportUnavailable", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("FILE_OPEN_RESULT FAILED를 받고도 %v 걸림(attach timeout을 기다림)", elapsed)
	}
	h.waitIdle(p)
}

func TestOpenResultFailedCodesMapLikeResultCodes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	p := h.fileV1Peer(sampleFS(), func(filetest.Open) filetest.Behavior { return filetest.Behavior{FailOpen: "PERMISSION_DENIED"} })
	if _, err := h.read(context.Background(), "main.py", 10); !errors.Is(err, workspacefile.ErrFilePermissionDenied) {
		t.Fatalf("Read() error = %v, want ErrFilePermissionDenied", err)
	}
	h.waitIdle(p)
}

// FILE_OPEN_RESULT SUCCEEDED는 통지일 뿐이다. 실제 Data WSS attach 없이는 요청을 성공시키지도 완료시키지도 않는다.
func TestOpenResultSucceededWithoutAttachDoesNotCompleteTheRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(o *Options) { o.AttachTimeout = 300 * time.Millisecond })
	p := h.fileV1Peer(sampleFS(), func(open filetest.Open) filetest.Behavior {
		return filetest.Behavior{NoAttach: true}
	})

	errc := make(chan error, 1)
	go func() {
		_, err := h.tree(context.Background(), "")
		errc <- err
	}()
	waitFor(t, "FILE_OPEN", func() bool { return len(p.Opens()) == 1 })
	open := p.Opens()[0]
	p.SendControl(filetest.Frame{
		"type": "FILE_OPEN_RESULT", "messageId": "m-1", "sentAt": time.Now().UTC().Format(time.RFC3339Nano),
		"replyToMessageId": open.MessageID, "fileRequestId": open.FileRequestID, "labInstanceId": open.LabInstanceID, "generation": open.Generation,
		"payload": map[string]any{"outcome": "SUCCEEDED"},
	})
	err := <-errc
	if !errors.Is(err, workspacefile.ErrTransportUnavailable) {
		t.Fatalf("Tree() error = %v, want ErrTransportUnavailable(attach timeout)", err)
	}
	h.waitIdle(p)
}

// 맞지 않는 FILE_OPEN_RESULT는 pending 요청을 실패시키지 않는다. 올바른 것만 실패시킨다.
func TestMismatchedOpenResultNeverCompletesThePendingRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(o *Options) { o.AttachTimeout = 3 * time.Second })
	p := h.fileV1Peer(sampleFS(), func(filetest.Open) filetest.Behavior { return filetest.Behavior{NoAttach: true} })

	errc := make(chan error, 1)
	go func() {
		_, err := h.tree(context.Background(), "")
		errc <- err
	}()
	waitFor(t, "FILE_OPEN", func() bool { return len(p.Opens()) == 1 })
	open := p.Opens()[0]
	result := func(mutate func(filetest.Frame)) filetest.Frame {
		f := filetest.Frame{
			"type": "FILE_OPEN_RESULT", "messageId": "m-1", "sentAt": time.Now().UTC().Format(time.RFC3339Nano),
			"replyToMessageId": open.MessageID, "fileRequestId": open.FileRequestID, "labInstanceId": open.LabInstanceID, "generation": open.Generation,
			"payload": map[string]any{"outcome": "FAILED", "error": map[string]any{"code": "UNAVAILABLE"}},
		}
		mutate(f)
		return f
	}
	for _, f := range []filetest.Frame{
		result(func(f filetest.Frame) { f["fileRequestId"] = "another-request" }),
		result(func(f filetest.Frame) { f["labInstanceId"] = "another-lab" }),
		result(func(f filetest.Frame) { f["generation"] = open.Generation + 1 }),
		result(func(f filetest.Frame) { f["replyToMessageId"] = "another-message" }),
	} {
		p.SendControl(f)
	}
	select {
	case err := <-errc:
		t.Fatalf("맞지 않는 FILE_OPEN_RESULT가 요청을 끝냄: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	p.SendControl(result(func(filetest.Frame) {}))
	select {
	case err := <-errc:
		if !errors.Is(err, workspacefile.ErrTransportUnavailable) {
			t.Fatalf("Tree() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("올바른 FILE_OPEN_RESULT가 요청을 끝내지 못함")
	}
	// 이 peer는 NoAttach로 아무것도 하지 않다가 FILE_OPEN_RESULT만 보냈다. Connector가 실패를 알렸으므로 SaaS는 FILE_CLOSE를 보내지
	// 않는다(peer 쪽 대기는 test 종료 때 정리된다). SaaS 쪽 상태만 확인한다.
	h.waitIdle(nil)
}

// attach가 기대한 correlation과 하나라도 다르면 요청을 건드리지 않고 그 connection만 거절한다. 요청은 시간 초과로 끝나고
// 다른 어떤 값으로도 완료되지 않는다.
func TestAttachWithWrongCorrelationIsRejectedAndNeverCompletesTheRequest(t *testing.T) {
	t.Parallel()
	cases := map[string]func(filetest.Frame){
		"다른 fileRequestId":    func(f filetest.Frame) { f["fileRequestId"] = "another-request" },
		"다른 labInstanceId":    func(f filetest.Frame) { f["labInstanceId"] = "another-lab" },
		"다른 generation":       func(f filetest.Frame) { f["generation"] = 2 },
		"generation 없음":       func(f filetest.Frame) { delete(f, "generation") },
		"다른 targetVmKey":      func(f filetest.Frame) { f["payload"].(map[string]any)["targetVmKey"] = "vk-db" },
		"다른 providerServerId": func(f filetest.Frame) { f["payload"].(map[string]any)["providerServerId"] = "srv-other" },
		"runtimeId 없음":        func(f filetest.Frame) { delete(f["payload"].(map[string]any), "runtimeId") },
		"targetVmKey 없음":      func(f filetest.Frame) { delete(f["payload"].(map[string]any), "targetVmKey") },
		"type이 ATTACH가 아님":    func(f filetest.Frame) { f["type"] = "FILE_DATA_ATTACHED" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, func(o *Options) { o.AttachTimeout = 300 * time.Millisecond })
			fs := sampleFS()
			p := h.fileV1Peer(fs, func(filetest.Open) filetest.Behavior { return filetest.Behavior{Attach: mutate} })

			if _, err := h.read(context.Background(), "main.py", 1024); !errors.Is(err, workspacefile.ErrTransportUnavailable) {
				t.Fatalf("Read() error = %v, want ErrTransportUnavailable", err)
			}
			if _, err := h.save(context.Background(), "main.py", fs.RevisionOf("main.py"), "changed"); !errors.Is(err, workspacefile.ErrTransportUnavailable) {
				t.Fatalf("Save() error = %v, want ErrTransportUnavailable", err)
			}
			// 거절된 attach는 요청 frame을 받지 못했다. 저장되지 않았다.
			if got, _ := fs.Content("main.py"); string(got) != "print('hello')\n" {
				t.Fatalf("거절된 attach 뒤 파일이 바뀜: %q", got)
			}
			for _, f := range p.DataFrames() {
				if f.FromSaaS && strings.Contains(string(f.Raw), "FILE_READ") {
					t.Fatalf("거절된 connection이 요청 frame을 받음: %s", f.Raw)
				}
			}
			h.waitIdle(p)
		})
	}
}

// 다른 Connector가 pending 요청의 fileRequestId를 알아도(여기서는 peer가 다른 Credential로 attach한다) 요청을 완료시키거나 취소하지 못한다.
func TestAnotherConnectorCannotAttachToAPendingRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(o *Options) { o.AttachTimeout = 300 * time.Millisecond })
	fs := sampleFS()
	p := h.fileV1Peer(fs, func(filetest.Open) filetest.Behavior { return filetest.Behavior{DataCredential: credentialB} })

	if _, err := h.read(context.Background(), "main.py", 1024); !errors.Is(err, workspacefile.ErrTransportUnavailable) {
		t.Fatalf("Read() error = %v, want ErrTransportUnavailable", err)
	}
	if !strings.Contains(h.logs.String(), "wrong_connector") {
		t.Fatalf("다른 Connector의 attach 거절 사유가 기록되지 않음:\n%s", h.logs.String())
	}
	for _, f := range p.DataFrames() {
		if f.FromSaaS && strings.Contains(string(f.Raw), "FILE_READ") {
			t.Fatal("다른 Connector의 connection이 요청 frame을 받음")
		}
	}
	h.waitIdle(p)
}

// 결과 frame의 correlation, 순서, type이 어긋나면 성공으로 처리하지 않고 연결을 종료한다. Save는 본문을 보낸 뒤이므로 저장 여부를 알 수 없다.
func TestResultFramesThatDoNotMatchTheRequestAreProtocolViolations(t *testing.T) {
	t.Parallel()
	cases := map[string]func(filetest.Frame){
		"다른 fileRequestId":    func(f filetest.Frame) { f["fileRequestId"] = "another-request" },
		"다른 labInstanceId":    func(f filetest.Frame) { f["labInstanceId"] = "another-lab" },
		"다른 generation":       func(f filetest.Frame) { f["generation"] = 99 },
		"다른 replyToMessageId": func(f filetest.Frame) { f["replyToMessageId"] = "another-message" },
		"replyToMessageId 없음": func(f filetest.Frame) { delete(f, "replyToMessageId") },
		"다른 작업의 result type": func(f filetest.Frame) {
			switch f["type"] {
			case "FILE_TREE_RESULT":
				f["type"] = "FILE_READ_RESULT"
			default:
				f["type"] = "FILE_TREE_RESULT"
			}
		},
		"ERROR frame":        func(f filetest.Frame) { f["type"] = "ERROR"; f["payload"] = map[string]any{"code": "INTERNAL_ERROR"} },
		"알 수 없는 type":        func(f filetest.Frame) { f["type"] = "FILE_DELETE_RESULT" },
		"generation 문자열":     func(f filetest.Frame) { f["generation"] = "3" },
		"messageId 없음":       func(f filetest.Frame) { delete(f, "messageId") },
		"sentAt 형식 오류":       func(f filetest.Frame) { f["sentAt"] = "yesterday" },
		"payload가 object 아님": func(f filetest.Frame) { f["payload"] = "x" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			fs := sampleFS()
			p := h.fileV1Peer(fs, func(filetest.Open) filetest.Behavior { return filetest.Behavior{Result: mutate} })
			ctx := context.Background()

			if _, err := h.tree(ctx, ""); !errors.Is(err, workspacefile.ErrTransportUnavailable) {
				t.Errorf("Tree() error = %v, want ErrTransportUnavailable", err)
			}
			if _, err := h.read(ctx, "main.py", 1024); !errors.Is(err, workspacefile.ErrTransportUnavailable) {
				t.Errorf("Read() error = %v, want ErrTransportUnavailable", err)
			}
			// Save 요청 본문은 이미 보냈고 Connector는 저장했다. 어긋난 결과로는 성공도 실패도 확정할 수 없다.
			_, err := h.save(ctx, "main.py", fs.RevisionOf("main.py"), "changed")
			if !errors.Is(err, workspacefile.ErrSaveOutcomeUnknown) {
				t.Errorf("Save() error = %v, want ErrSaveOutcomeUnknown", err)
			}
			h.waitIdle(p)
		})
	}
}

// Schema를 만족하지 않는 결과는 성공으로 처리하지 않는다.
func TestMalformedResultPayloadsAreRejected(t *testing.T) {
	t.Parallel()
	setPayload := func(mutate func(map[string]any)) func(filetest.Frame) {
		return func(f filetest.Frame) { mutate(f["payload"].(map[string]any)) }
	}
	failed := func(error any) func(filetest.Frame) {
		return func(f filetest.Frame) {
			p := map[string]any{"outcome": "FAILED"}
			if error != nil {
				p["error"] = error
			}
			f["payload"] = p
		}
	}
	type kase struct {
		name   string
		op     string // tree, read, save
		mutate func(filetest.Frame)
	}
	cases := []kase{
		// 공통
		{"outcome 없음", "read", setPayload(func(p map[string]any) { delete(p, "outcome") })},
		{"outcome 알 수 없음", "read", setPayload(func(p map[string]any) { p["outcome"] = "OK" })},
		{"outcome 소문자", "read", setPayload(func(p map[string]any) { p["outcome"] = "succeeded" })},
		{"FAILED인데 error 없음", "read", failed(nil)},
		{"FAILED error가 object가 아님", "read", failed("x")},
		{"FAILED error.code 없음", "read", failed(map[string]any{})},
		{"FAILED error.code 빈 문자열", "read", failed(map[string]any{"code": ""})},
		{"FAILED error.message가 문자열이 아님", "read", failed(map[string]any{"code": "UNAVAILABLE", "message": 7})},
		// Tree
		{"Tree entries 없음", "tree", setPayload(func(p map[string]any) { delete(p, "entries") })},
		{"Tree entries가 배열이 아님", "tree", setPayload(func(p map[string]any) { p["entries"] = "x" })},
		{"Tree entries null", "tree", setPayload(func(p map[string]any) { p["entries"] = nil })},
		{"Tree entry가 object가 아님", "tree", setPayload(func(p map[string]any) { p["entries"] = []any{"a"} })},
		{"Tree entry name 없음", "tree", setPayload(func(p map[string]any) { p["entries"] = []any{map[string]any{"kind": "file"}} })},
		{"Tree entry name 빈 문자열", "tree", setPayload(func(p map[string]any) { p["entries"] = []any{map[string]any{"name": "", "kind": "file"}} })},
		{"Tree entry kind 없음", "tree", setPayload(func(p map[string]any) { p["entries"] = []any{map[string]any{"name": "a"}} })},
		{"Tree entry kind 알 수 없음", "tree", setPayload(func(p map[string]any) { p["entries"] = []any{map[string]any{"name": "a", "kind": "symlink"}} })},
		{"Tree entry kind 대문자", "tree", setPayload(func(p map[string]any) { p["entries"] = []any{map[string]any{"name": "a", "kind": "FILE"}} })},
		// Read
		{"Read revision 없음", "read", setPayload(func(p map[string]any) { delete(p, "revision") })},
		{"Read revision 형식 위반", "read", setPayload(func(p map[string]any) { p["revision"] = `a"b` })},
		{"Read revision 너무 김", "read", setPayload(func(p map[string]any) { p["revision"] = strings.Repeat("a", 129) })},
		{"Read revision 빈 문자열", "read", setPayload(func(p map[string]any) { p["revision"] = "" })},
		{"Read size 없음", "read", setPayload(func(p map[string]any) { delete(p, "size") })},
		{"Read size 문자열", "read", setPayload(func(p map[string]any) { p["size"] = "3" })},
		{"Read size 음수", "read", setPayload(func(p map[string]any) { p["size"] = -1 })},
		{"Read size 소수", "read", setPayload(func(p map[string]any) { p["size"] = 1.5 })},
		{"Read size가 본문보다 작음", "read", setPayload(func(p map[string]any) { p["size"] = p["size"].(int) - 1 })},
		{"Read size가 본문보다 큼", "read", setPayload(func(p map[string]any) { p["size"] = p["size"].(int) + 1 })},
		// Save
		{"Save revision 없음", "save", setPayload(func(p map[string]any) { delete(p, "revision") })},
		{"Save revision 형식 위반", "save", setPayload(func(p map[string]any) { p["revision"] = "a b" })},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			fs := sampleFS()
			p := h.fileV1Peer(fs, func(filetest.Open) filetest.Behavior { return filetest.Behavior{Result: tc.mutate} })
			ctx := context.Background()

			var err error
			switch tc.op {
			case "tree":
				_, err = h.tree(ctx, "")
			case "read":
				_, err = h.read(ctx, "main.py", 1024)
			default:
				_, err = h.save(ctx, "main.py", fs.RevisionOf("main.py"), "changed")
			}
			want := workspacefile.ErrTransportUnavailable
			if tc.op == "save" {
				want = workspacefile.ErrSaveOutcomeUnknown
			}
			if err == nil {
				t.Fatal("Schema를 만족하지 않는 결과를 성공으로 처리함")
			}
			// FAILED 결과의 error.code가 유효하지 않은 경우도 포함해 사용 불가 계열이어야 한다. 확정된 업무 오류가 아니다.
			if !errors.Is(err, want) {
				t.Fatalf("error = %v, want %v", err, want)
			}
			h.waitIdle(p)
		})
	}
}

// 한도를 넘는 크기를 선언하면 본문 Binary frame을 읽지 않고 한도 초과로 처리한다.
func TestReadThatDeclaresMoreThanTheLimitIsTooLarge(t *testing.T) {
	t.Parallel()
	for name, size := range map[string]any{"한도를 넘는 크기": 5000, "int64를 넘는 크기": 1e30} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			p := h.fileV1Peer(sampleFS(), func(filetest.Open) filetest.Behavior {
				return filetest.Behavior{Result: func(f filetest.Frame) { f["payload"].(map[string]any)["size"] = size }}
			})
			if _, err := h.read(context.Background(), "main.py", 64); !errors.Is(err, workspacefile.ErrTooLarge) {
				t.Fatalf("Read() error = %v, want ErrTooLarge", err)
			}
			h.waitIdle(p)
		})
	}
}

// 한도(maxBytes)와 정확히 같은 크기는 받아들이고, 본문이 선언한 크기와 다르면 거절한다.
func TestReadSizeBoundaries(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fs := filetest.NewFS()
	fs.Put("exact.txt", bytes.Repeat([]byte("a"), 16))
	fs.Put("over.txt", bytes.Repeat([]byte("a"), 17))
	h.fileV1Peer(fs, nil)

	if data, err := h.read(context.Background(), "exact.txt", 16); err != nil || len(data.Content) != 16 {
		t.Fatalf("Read(exact) = %d bytes, %v", len(data.Content), err)
	}
	if _, err := h.read(context.Background(), "over.txt", 16); !errors.Is(err, workspacefile.ErrTooLarge) {
		t.Fatalf("Read(over) error = %v, want ErrTooLarge", err)
	}
}

// 파일 경로는 canonical 상태로 Data WSS에 도착한다. 이 transport는 경로를 다시 해석하지 않는다.
func TestPathReachesTheConnectorVerbatim(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fs := filetest.NewFS()
	fs.Put("a b/한글 파일.txt", []byte("x"))
	p := h.fileV1Peer(fs, nil)
	if _, err := h.read(context.Background(), "a b/한글 파일.txt", 10); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	var request map[string]any
	for _, f := range p.DataFrames() {
		if f.FromSaaS && !f.Binary && strings.Contains(string(f.Raw), `"FILE_READ"`) {
			if err := json.Unmarshal(f.Raw, &request); err != nil {
				t.Fatal(err)
			}
		}
	}
	payload, _ := request["payload"].(map[string]any)
	if payload["path"] != "a b/한글 파일.txt" || payload["maxBytes"] != float64(10) {
		t.Fatalf("FILE_READ payload = %v", payload)
	}
	for _, key := range []string{"fileRequestId", "labInstanceId", "generation"} {
		if request[key] == nil {
			t.Fatalf("FILE_READ에 %s 없음: %v", key, request)
		}
	}
	h.waitIdle(p)
}

// Router/Registry는 요청 correlation을 연결 수명 동안만 들고 있다. 성공한 요청 뒤에는 어떤 상태도 남지 않는다.
func TestNoStateRemainsAfterSuccessfulRequests(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	p := h.fileV1Peer(sampleFS(), nil)
	for i := 0; i < 5; i++ {
		if _, err := h.tree(context.Background(), ""); err != nil {
			t.Fatal(err)
		}
	}
	h.waitIdle(p)
	if h.router.PendingFileOpens(h.principalA.ConnectorID) != 0 || h.broker.PendingCount() != 0 {
		t.Fatal("pending이 남음")
	}
}

var _ connector.FileSink = (*Broker)(nil)
