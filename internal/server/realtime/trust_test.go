package realtime

import (
	"sync"
	"testing"
)

func conn(credentialID, connectorID string) *dataConn {
	return &dataConn{credentialID: credentialID, connectorID: connectorID}
}

// 인증을 시작한 뒤(begin) 같은 Credential의 revoke가 통지되면 그 요청은 등록(admit)에서 거절된다.
// 통지 뒤에 시작한 요청이나 다른 Credential/Connector의 요청은 거절하지 않는다.
func TestDataTrustAdmitRejectsOnlyRequestsThatAuthenticatedBeforeTheRevoke(t *testing.T) {
	tr := newDataTrust()

	inFlight := tr.begin()       // 인증을 시작했다.
	other := tr.begin()          // 다른 Credential의 요청도 진행 중이다.
	otherConnector := tr.begin() // 다른 Connector의 요청이다.
	tr.revokeCredential("cred-1")
	afterNotice := tr.begin() // 통지 뒤에 시작했다. 저장소는 이미 revoke되어 있으므로 실제로는 인증에서 실패한다.

	if tr.admit(inFlight, conn("cred-1", "connector-1")) {
		t.Fatal("인증 시작 뒤에 revoke된 Credential의 요청이 등록됨")
	}
	if !tr.admit(other, conn("cred-1b", "connector-1")) {
		t.Fatal("같은 Connector의 다른 Credential 요청이 거절됨")
	}
	if !tr.admit(otherConnector, conn("cred-2", "connector-2")) {
		t.Fatal("다른 Connector의 요청이 거절됨")
	}
	if !tr.admit(afterNotice, conn("cred-1", "connector-1")) {
		t.Fatal("통지 뒤에 시작한 요청이 거절됨(이 요청의 인증은 revoke된 저장소 상태를 본다)")
	}
}

// Connector revoke도 같은 규칙이다. Credential과 무관하게 그 Connector의 요청이 거절된다.
func TestDataTrustAdmitRejectsRequestsOfARevokedConnectorWhoseAuthenticationWasInFlight(t *testing.T) {
	tr := newDataTrust()
	a, b := tr.begin(), tr.begin()
	tr.revokeConnector("connector-1")

	if tr.admit(a, conn("cred-1", "connector-1")) || tr.admit(b, conn("cred-1b", "connector-1")) {
		t.Fatal("revoke된 Connector의 요청이 등록됨")
	}
}

// 등록된 connection은 revoke에 돌려주고 추적에서 뺀다. 일치하지 않는 connection은 그대로 둔다.
func TestDataTrustRevokeReturnsOnlyMatchingConnections(t *testing.T) {
	tr := newDataTrust()
	first, sameConnector, other := conn("cred-1", "connector-1"), conn("cred-1b", "connector-1"), conn("cred-2", "connector-2")
	for _, d := range []*dataConn{first, sameConnector, other} {
		if !tr.admit(tr.begin(), d) {
			t.Fatal("등록 실패")
		}
	}

	got := tr.revokeCredential("cred-1")
	if len(got) != 1 || got[0] != first || !first.revoked.Load() {
		t.Fatalf("revokeCredential() = %v, want [first] 그리고 revoked 표시", got)
	}
	if sameConnector.revoked.Load() || other.revoked.Load() {
		t.Fatal("다른 Credential/Connector의 connection이 revoked로 표시됨")
	}
	if got := tr.size(); got != 2 {
		t.Fatalf("size() = %d, want 2", got)
	}

	if got := tr.revokeConnector("connector-1"); len(got) != 1 || got[0] != sameConnector {
		t.Fatalf("revokeConnector() = %v, want [sameConnector]", got)
	}
	if got := tr.revokeCredential("cred-1"); len(got) != 0 {
		t.Fatalf("이미 revoke한 Credential을 다시 revoke = %v, want 없음(멱등)", got)
	}
}

// 표식(tombstone)은 인증 중인 요청이 있는 동안에만 둔다. 요청이 모두 끝나면 버려 무한히 쌓이지 않는다.
func TestDataTrustDropsTombstonesWhenNoAuthenticationIsInFlight(t *testing.T) {
	tr := newDataTrust()

	// 진행 중인 요청이 없으면 표식을 만들지 않는다.
	tr.revokeCredential("cred-idle")
	tr.revokeConnector("connector-idle")
	if len(tr.revokedCredentials)+len(tr.revokedConnectors) != 0 {
		t.Fatal("진행 중인 요청이 없는데 표식을 만듦")
	}

	inFlight := tr.begin()
	tr.revokeCredential("cred-1")
	tr.revokeConnector("connector-1")
	if len(tr.revokedCredentials) != 1 || len(tr.revokedConnectors) != 1 {
		t.Fatalf("표식 = %d/%d, want 1/1", len(tr.revokedCredentials), len(tr.revokedConnectors))
	}
	inFlight.release()
	if len(tr.revokedCredentials)+len(tr.revokedConnectors) != 0 || tr.inflight != 0 {
		t.Fatalf("요청이 끝났는데 표식 %d/%d, inflight %d가 남음", len(tr.revokedCredentials), len(tr.revokedConnectors), tr.inflight)
	}
}

// release와 admit은 합쳐서 한 번만 요청을 끝낸다. 여러 번 호출해도 inflight가 음수가 되거나 다른 요청의 표식을 일찍 버리지 않는다.
func TestDataTrustTicketIsEndedOnlyOnce(t *testing.T) {
	tr := newDataTrust()
	first, second := tr.begin(), tr.begin()

	first.release()
	first.release()
	if tr.admit(first, conn("cred-1", "connector-1")) {
		t.Fatal("이미 끝난 ticket으로 등록됨")
	}
	if tr.inflight != 1 {
		t.Fatalf("inflight = %d, want 1(두 번째 요청이 진행 중)", tr.inflight)
	}

	tr.revokeCredential("cred-1")
	if len(tr.revokedCredentials) != 1 {
		t.Fatal("진행 중인 요청이 있는데 표식이 없음")
	}
	if tr.admit(second, conn("cred-1", "connector-1")) {
		t.Fatal("진행 중이던 요청이 revoke된 Credential로 등록됨")
	}
	if tr.inflight != 0 {
		t.Fatalf("inflight = %d, want 0", tr.inflight)
	}
}

// 이미 등록된 connection이 forget으로 빠진 뒤에는 revoke 대상이 아니다.
func TestDataTrustForgetRemovesConnection(t *testing.T) {
	tr := newDataTrust()
	d := conn("cred-1", "connector-1")
	tr.admit(tr.begin(), d)
	tr.forget(d)
	tr.forget(d)
	if got := tr.revokeCredential("cred-1"); len(got) != 0 || d.revoked.Load() {
		t.Fatalf("forget한 connection이 revoke됨: %v", got)
	}
}

// begin/admit/release/revoke/forget이 동시에 일어나도 -race에서 안전하고 불변식이 유지된다.
// 저장소 revoke는 통지 전에 끝나므로, 통지 뒤에 시작한 요청은 인증에 실패한다고 모델링해 그 요청은 admit하지 않는다.
func TestDataTrustConcurrentUseKeepsInvariants(t *testing.T) {
	tr := newDataTrust()
	var revokedAt sync.Map // credential → revoke 통지가 끝났는지
	var wg sync.WaitGroup
	const workers = 16
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				ticket := tr.begin()
				if _, revoked := revokedAt.Load("cred-x"); revoked {
					ticket.release() // 저장소 revoke 뒤의 인증은 실패한다.
					continue
				}
				d := conn("cred-x", "connector-1")
				if tr.admit(ticket, d) {
					if d.revoked.Load() {
						t.Error("등록된 connection이 이미 revoked임")
					}
					tr.forget(d)
				} else if !d.revoked.Load() {
					t.Error("거절된 connection이 revoked로 표시되지 않음")
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			tr.revokeCredential("cred-other")
		}
		revokedAt.Store("cred-x", true)
		tr.revokeCredential("cred-x")
	}()
	wg.Wait()

	if tr.inflight != 0 || len(tr.revokedCredentials)+len(tr.revokedConnectors) != 0 || tr.size() != 0 {
		t.Fatalf("끝난 뒤 inflight=%d 표식=%d/%d 등록=%d, want 모두 0", tr.inflight, len(tr.revokedCredentials), len(tr.revokedConnectors), tr.size())
	}
}
