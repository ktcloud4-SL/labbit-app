package filetransport

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/server/filetransport/filetest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/workspacefile"
)

// FILE_CLOSE를 받은 reason을 기다린다.
func waitClose(t *testing.T, p *filetest.Peer, reason string) {
	t.Helper()
	waitFor(t, "FILE_CLOSE "+reason, func() bool {
		for _, c := range p.Closes() {
			if c.Reason == reason {
				return true
			}
		}
		return false
	})
}

// attach가 시간 안에 오지 않으면 요청은 사용 불가로 끝나고 Connector에 FILE_CLOSE를 보낸다. 어떤 상태도 남지 않는다.
func TestAttachTimeoutEndsTheRequestAndCleansUp(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(o *Options) { o.AttachTimeout = 200 * time.Millisecond })
	p := h.fileV1Peer(sampleFS(), func(filetest.Open) filetest.Behavior { return filetest.Behavior{NoAttach: true} })

	for name, call := range map[string]func() error{
		"Tree": func() error { _, err := h.tree(context.Background(), ""); return err },
		"Read": func() error { _, err := h.read(context.Background(), "main.py", 10); return err },
		// attach 전이므로 Save 본문은 보내지 않았다. 저장 여부가 불명확한 상태가 아니다.
		"Save": func() error { _, err := h.save(context.Background(), "main.py", "r1", "x"); return err },
	} {
		err := call()
		if !errors.Is(err, workspacefile.ErrTransportUnavailable) || errors.Is(err, workspacefile.ErrSaveOutcomeUnknown) {
			t.Errorf("%s: error = %v, want ErrTransportUnavailable", name, err)
		}
	}
	waitClose(t, p, "REQUEST_TIMEOUT")
	h.waitIdle(p)
}

// attach 뒤 결과가 시간 안에 오지 않으면 요청은 실패한다. Save는 본문을 보냈으므로 저장 여부를 알 수 없고 자동으로 다시 보내지 않는다.
func TestOperationTimeoutEndsTheRequestAndCleansUp(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(o *Options) { o.OperationTimeout = 200 * time.Millisecond })
	stall := make(chan struct{})
	t.Cleanup(func() { close(stall) })
	fs := sampleFS()
	p := h.fileV1Peer(fs, func(filetest.Open) filetest.Behavior { return filetest.Behavior{Stall: stall} })

	if _, err := h.tree(context.Background(), ""); !errors.Is(err, workspacefile.ErrTransportUnavailable) {
		t.Fatalf("Tree() error = %v, want ErrTransportUnavailable", err)
	}
	if _, err := h.read(context.Background(), "main.py", 100); !errors.Is(err, workspacefile.ErrTransportUnavailable) {
		t.Fatalf("Read() error = %v, want ErrTransportUnavailable", err)
	}
	_, err := h.save(context.Background(), "main.py", fs.RevisionOf("main.py"), "changed")
	if !errors.Is(err, workspacefile.ErrSaveOutcomeUnknown) {
		t.Fatalf("Save() error = %v, want ErrSaveOutcomeUnknown", err)
	}
	waitClose(t, p, "REQUEST_TIMEOUT")
	h.waitIdle(p)

	// 같은 Save를 자동으로 다시 보내지 않았다. FILE_OPEN은 요청마다 정확히 하나다.
	saves := 0
	for _, open := range p.Opens() {
		if open.Operation == "SAVE" {
			saves++
		}
	}
	if saves != 1 {
		t.Fatalf("Save FILE_OPEN %d개, want 1(자동 재전송 금지)", saves)
	}
}

// HTTP 요청이 취소되면 기다리는 요청은 취소 오류로 끝나고 Connector에 FILE_CLOSE를 보내며 어떤 상태도 남지 않는다.
func TestCancelWhileWaitingForAttach(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	p := h.fileV1Peer(sampleFS(), func(filetest.Open) filetest.Behavior { return filetest.Behavior{NoAttach: true} })

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := h.tree(ctx, "")
		errc <- err
	}()
	waitFor(t, "FILE_OPEN", func() bool { return len(p.Opens()) == 1 })
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("Tree() error = %v, want context.Canceled", err)
	}
	waitClose(t, p, "REQUEST_CANCELED")
	h.waitIdle(p)
}

func TestCancelWhileWaitingForTheResult(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	stall := make(chan struct{})
	t.Cleanup(func() { close(stall) })
	p := h.fileV1Peer(sampleFS(), func(filetest.Open) filetest.Behavior { return filetest.Behavior{Stall: stall} })

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := h.read(ctx, "main.py", 100)
		errc <- err
	}()
	// attach되어 요청 frame을 받을 때까지 기다린다.
	waitFor(t, "request frame", func() bool {
		for _, f := range p.DataFrames() {
			if f.FromSaaS && strings.Contains(string(f.Raw), `"FILE_READ"`) {
				return true
			}
		}
		return false
	})
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("Read() error = %v, want context.Canceled", err)
	}
	waitClose(t, p, "REQUEST_CANCELED")
	h.waitIdle(p)
}

// 이미 취소된 context는 FILE_OPEN을 보내지도 않는다.
func TestCanceledContextSendsNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	p := h.fileV1Peer(sampleFS(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.tree(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("Tree() error = %v, want context.Canceled", err)
	}
	if len(p.Opens()) != 0 {
		t.Fatal("취소된 요청이 FILE_OPEN을 보냄")
	}
	h.waitIdle(p)
}

// 요청을 읽은 뒤 결과 없이 Data WSS가 끊기면 요청은 실패한다. 자동으로 재연결하거나 다시 보내지 않는다.
func TestDisconnectAfterTheRequestFailsWithoutRetry(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fs := sampleFS()
	p := h.fileV1Peer(fs, func(filetest.Open) filetest.Behavior { return filetest.Behavior{Drop: true} })
	ctx := context.Background()

	if _, err := h.tree(ctx, ""); !errors.Is(err, workspacefile.ErrTransportUnavailable) {
		t.Fatalf("Tree() error = %v, want ErrTransportUnavailable", err)
	}
	if _, err := h.read(ctx, "main.py", 100); !errors.Is(err, workspacefile.ErrTransportUnavailable) {
		t.Fatalf("Read() error = %v, want ErrTransportUnavailable", err)
	}
	if _, err := h.save(ctx, "main.py", fs.RevisionOf("main.py"), "changed"); !errors.Is(err, workspacefile.ErrSaveOutcomeUnknown) {
		t.Fatalf("Save() error = %v, want ErrSaveOutcomeUnknown", err)
	}
	h.waitIdle(p)
	if got := len(p.Opens()); got != 3 {
		t.Fatalf("FILE_OPEN %d개, want 3(자동 재전송 금지)", got)
	}
}

// 한 Connector에 동시에 여러 요청이 진행돼도 서로 섞이지 않는다. 요청마다 독립된 fileRequestId와 Data WSS를 쓴다.
func TestConcurrentRequestsAreIsolated(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fs := filetest.NewFS()
	const n = 12
	for i := 0; i < n; i++ {
		fs.Put(fmt.Sprintf("f%02d.txt", i), []byte(fmt.Sprintf("content-%02d", i)))
		fs.Put(fmt.Sprintf("s%02d.txt", i), []byte("before"))
	}
	p := h.fileV1Peer(fs, nil)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, 3*n)
	for i := 0; i < n; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			data, err := h.read(ctx, fmt.Sprintf("f%02d.txt", i), 100)
			if err != nil || string(data.Content) != fmt.Sprintf("content-%02d", i) {
				errs <- fmt.Errorf("Read(f%02d) = %q, %v", i, data.Content, err)
			}
		}()
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("s%02d.txt", i)
			if _, err := h.save(ctx, name, fs.RevisionOf(name), fmt.Sprintf("after-%02d", i)); err != nil {
				errs <- fmt.Errorf("Save(%s) error = %v", name, err)
			}
		}()
		go func() {
			defer wg.Done()
			entries, err := h.tree(ctx, "")
			if err != nil || len(entries) != 2*n {
				errs <- fmt.Errorf("Tree() = %d entries, %v", len(entries), err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	for i := 0; i < n; i++ {
		if got, _ := fs.Content(fmt.Sprintf("s%02d.txt", i)); string(got) != fmt.Sprintf("after-%02d", i) {
			t.Errorf("s%02d.txt = %q", i, got)
		}
	}
	ids := map[string]bool{}
	for _, open := range p.Opens() {
		if ids[open.FileRequestID] {
			t.Fatalf("fileRequestId %q가 중복됨", open.FileRequestID)
		}
		ids[open.FileRequestID] = true
	}
	if len(ids) != 3*n {
		t.Fatalf("FILE_OPEN %d개, want %d", len(ids), 3*n)
	}
	h.waitIdle(p)
}

// 느리거나 멈춘 요청 하나가 다른 요청을 막지 않고, 다른 요청의 결과를 가져가지도 않는다.
func TestAStalledRequestDoesNotBlockOrStealFromOthers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fs := sampleFS()
	stall := make(chan struct{})
	var mu sync.Mutex
	first := ""
	p := h.fileV1Peer(fs, func(open filetest.Open) filetest.Behavior {
		mu.Lock()
		defer mu.Unlock()
		if first == "" {
			first = open.FileRequestID
			return filetest.Behavior{Stall: stall}
		}
		return filetest.Behavior{}
	})
	ctx := context.Background()

	stalled := make(chan error, 1)
	var stalledData workspacefile.FileData
	go func() {
		var err error
		stalledData, err = h.read(ctx, "main.py", 100)
		stalled <- err
	}()
	waitFor(t, "first FILE_OPEN", func() bool { return len(p.Opens()) == 1 })

	// 멈춘 요청이 있어도 다른 요청은 자기 결과를 받는다.
	data, err := h.read(ctx, "src/app.py", 100)
	if err != nil || string(data.Content) != "def main():\n    pass\n" {
		t.Fatalf("다른 Read() = %q, %v", data.Content, err)
	}
	select {
	case err := <-stalled:
		t.Fatalf("멈춘 요청이 다른 요청의 결과로 끝남: %v", err)
	default:
	}
	close(stall)
	if err := <-stalled; err != nil || string(stalledData.Content) != "print('hello')\n" {
		t.Fatalf("멈췄던 Read() = %q, %v", stalledData.Content, err)
	}
	h.waitIdle(p)
}

// Credential이 revoke되면 그 Credential로 인증된 File Data WSS를 닫고 진행 중인 요청은 실패한다. 이후 같은 Credential의 Upgrade는 거절된다.
func TestCredentialRevokeEndsInFlightRequestsAndRejectsNewUpgrades(t *testing.T) {
	t.Parallel()
	for name, revoke := range map[string]func(*harness){
		"Credential": func(h *harness) {
			h.auth.revoke(h.principalA.CredentialID)
			h.registry.RevokeCredential(h.principalA.CredentialID)
		},
		"Connector": func(h *harness) {
			h.auth.revoke(h.principalA.CredentialID)
			h.registry.RevokeConnector(h.principalA.ConnectorID)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			fs := sampleFS()
			stall := make(chan struct{})
			t.Cleanup(func() { close(stall) })
			p := h.fileV1Peer(fs, func(filetest.Open) filetest.Behavior { return filetest.Behavior{Stall: stall} })

			readErr := make(chan error, 1)
			go func() {
				_, err := h.read(context.Background(), "main.py", 100)
				readErr <- err
			}()
			saveErr := make(chan error, 1)
			go func() {
				_, err := h.save(context.Background(), "main.py", fs.RevisionOf("main.py"), "changed")
				saveErr <- err
			}()
			waitFor(t, "두 요청이 attach됨", func() bool { return h.broker.trust.size() == 2 })
			waitFor(t, "두 요청이 요청 frame을 받음", func() bool {
				requests := 0
				for _, f := range p.DataFrames() {
					if f.FromSaaS && !f.Binary && (strings.Contains(string(f.Raw), `"FILE_READ"`) || strings.Contains(string(f.Raw), `"FILE_SAVE"`)) {
						requests++
					}
				}
				return requests == 2
			})

			revoke(h)
			if err := <-readErr; !errors.Is(err, workspacefile.ErrTransportUnavailable) {
				t.Fatalf("Read() error = %v, want ErrTransportUnavailable", err)
			}
			// Save는 본문을 보낸 뒤이므로 저장 여부를 알 수 없다.
			if err := <-saveErr; !errors.Is(err, workspacefile.ErrSaveOutcomeUnknown) {
				t.Fatalf("Save() error = %v, want ErrSaveOutcomeUnknown", err)
			}
			if h.broker.trust.size() != 0 {
				t.Fatal("revoke된 Data WSS가 추적 목록에 남음")
			}
			// 계약(README §2): revoke된 Credential의 Data WSS는 close code 4001로 종료한다.
			waitFor(t, "두 Data WSS가 4001로 종료", func() bool {
				codes := 0
				for _, code := range p.DataCloseCodes() {
					if code == 4001 {
						codes++
					}
				}
				return codes == 2
			})

			// revoke된 Credential은 새 Upgrade가 거절된다(401).
			_, resp, err := h.rawData(credentialA, "labbit.connector-file.v1")
			if err == nil || resp == nil || resp.StatusCode != 401 {
				t.Fatalf("revoke된 Credential의 Upgrade = %v, %v, want 401", resp, err)
			}
		})
	}
}

// 다른 Credential의 Data WSS는 revoke의 영향을 받지 않는다.
func TestRevokeOnlyAffectsTheRevokedCredential(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.registry.RevokeCredential(h.principalB.CredentialID) // 연결이 없어도 통지는 안전하다.
	p := h.fileV1Peer(sampleFS(), nil)
	if _, err := h.tree(context.Background(), ""); err != nil {
		t.Fatalf("다른 Credential의 revoke가 요청을 막음: %v", err)
	}
	h.waitIdle(p)
}

// Upgrade 중 trust가 revoke되면 오래된 trust로 connection을 열어 두지 않는다(인증과 등록 사이의 경쟁).
func TestRevokeBetweenAuthenticationAndAdmissionRejectsTheConnection(t *testing.T) {
	t.Parallel()
	trust := newDataTrust()
	ticket := trust.begin()
	d := &dataConn{credentialID: h2uuid(1), connectorID: h2uuid(2)}
	trust.revokeCredential(d.credentialID) // 인증을 시작한 뒤 통지된 revoke다.
	if trust.admit(ticket, d) {
		t.Fatal("인증을 시작한 뒤 revoke된 Credential의 connection이 등록됨")
	}
	if !d.revoked.Load() || trust.size() != 0 {
		t.Fatal("revoke된 connection이 추적됨")
	}

	// 통지 뒤에 시작한 요청은 영향을 받지 않는다(저장소는 이미 revoke 상태라 인증이 실패한다).
	later := trust.begin()
	d2 := &dataConn{credentialID: d.credentialID, connectorID: d.connectorID}
	if !trust.admit(later, d2) {
		t.Fatal("revoke 뒤에 시작한 요청의 connection이 거절됨")
	}
	trust.forget(d2)
	// 표식은 인증 중인 요청이 없으면 버린다.
	if len(trust.revokedCredentials) != 0 || len(trust.revokedConnectors) != 0 {
		t.Fatalf("표식이 남음: %v %v", trust.revokedCredentials, trust.revokedConnectors)
	}
}

// 서비스가 종료되면 새 요청과 Upgrade를 거절하고 진행 중인 요청을 끝낸다. Connector에는 FILE_CLOSE로 알린다.
func TestShutdownEndsInFlightRequestsAndRejectsNewOnes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	stall := make(chan struct{})
	t.Cleanup(func() { close(stall) })
	p := h.fileV1Peer(sampleFS(), func(filetest.Open) filetest.Behavior { return filetest.Behavior{Stall: stall} })

	errc := make(chan error, 1)
	go func() {
		_, err := h.read(context.Background(), "main.py", 100)
		errc <- err
	}()
	waitFor(t, "attach", func() bool { return h.broker.trust.size() == 1 })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.broker.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := <-errc; !errors.Is(err, workspacefile.ErrTransportUnavailable) {
		t.Fatalf("Read() error = %v, want ErrTransportUnavailable", err)
	}
	waitClose(t, p, "SERVICE_RESTARTING")

	// 이후 요청은 FILE_OPEN을 보내지 않고 거절된다. 새 Upgrade도 거절된다.
	opens := len(p.Opens())
	if _, err := h.tree(context.Background(), ""); !errors.Is(err, workspacefile.ErrTransportUnavailable) {
		t.Fatalf("종료 뒤 Tree() error = %v", err)
	}
	if len(p.Opens()) != opens {
		t.Fatal("종료 뒤 요청이 FILE_OPEN을 보냄")
	}
	if _, resp, err := h.rawData(credentialA, "labbit.connector-file.v1"); err == nil || resp == nil || resp.StatusCode != 503 {
		t.Fatalf("종료 뒤 Upgrade = %v, %v, want 503", resp, err)
	}
	if h.broker.PendingCount() != 0 {
		t.Fatal("pending이 남음")
	}
}

func TestOptionsAreValidated(t *testing.T) {
	t.Parallel()
	if _, err := New(Options{Auth: &fakeAuth{}}); err == nil {
		t.Error("Control 없이 만들어짐")
	}
	h := newHarness(t)
	if _, err := New(Options{Control: h.router}); err == nil {
		t.Error("Auth 없이 만들어짐")
	}
	if _, err := New(Options{Control: h.router, Auth: &fakeAuth{}, AttachTimeout: -1}); err == nil {
		t.Error("음수 timeout으로 만들어짐")
	}
	if _, err := h.broker.Read(context.Background(), h.target(), mustFile(t, "a"), 0); err == nil {
		t.Error("maxBytes 0으로 Read가 허용됨")
	}
}

func mustFile(t *testing.T, raw string) workspacefile.Path {
	t.Helper()
	p, err := workspacefile.ParseFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
