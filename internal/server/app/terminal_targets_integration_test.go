//go:build integration

package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
)

func (e *terminalEnv) targetsPath(labInstanceID string) string {
	return "/api/v1/lab-instances/" + labInstanceID + "/terminal-targets"
}

// targetList는 GET terminal-targets 응답이다. 필드를 하나씩 확인하기 위해 map으로도 읽는다.
type targetList struct {
	Generation     int64
	WorkspaceVMKey string
	Items          []targetItem
}

type targetItem struct {
	VMKey         string
	Role          string
	InstanceIndex int64
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// decodeTargets는 200 응답을 OpenAPI TerminalTargetList의 field 집합과 정확히 대조해 읽는다.
// 계약에 없는 field(Provider ID, Connector ID, 주소 등)가 하나라도 있으면 실패한다.
func (e *terminalEnv) decodeTargets(resp apiResponse) targetList {
	e.t.Helper()
	if resp.Status != http.StatusOK {
		e.t.Fatalf("GET terminal-targets = %d, want 200: %s", resp.Status, resp.Body)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		e.t.Fatalf("Content-Type = %q", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		e.t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	body := resp.json(e.t)
	if got := fmt.Sprint(keysOf(body)); got != "[generation items workspaceVmKey]" {
		e.t.Fatalf("응답 field = %s, want exactly generation/workspaceVmKey/items: %s", got, resp.Body)
	}
	list := targetList{Generation: int64(body["generation"].(float64)), WorkspaceVMKey: body["workspaceVmKey"].(string)}
	items, ok := body["items"].([]any)
	if !ok || len(items) == 0 {
		e.t.Fatalf("items = %v, want a non-empty array", body["items"])
	}
	for _, raw := range items {
		item := raw.(map[string]any)
		if got := fmt.Sprint(keysOf(item)); got != "[instanceIndex role vmKey]" {
			e.t.Fatalf("item field = %s, want exactly vmKey/role/instanceIndex: %v", got, item)
		}
		list.Items = append(list.Items, targetItem{VMKey: item["vmKey"].(string), Role: item["role"].(string), InstanceIndex: int64(item["instanceIndex"].(float64))})
	}
	return list
}

func (l targetList) item(t *testing.T, vmKey string) targetItem {
	t.Helper()
	for _, item := range l.Items {
		if item.VMKey == vmKey {
			return item
		}
	}
	t.Fatalf("items에 %q가 없음: %+v", vmKey, l.Items)
	return targetItem{}
}

func createBodyFor(vmKey string) string {
	return fmt.Sprintf(`{"targetVmKey":%q,"cols":120,"rows":40}`, vmKey)
}

// Browser가 공식 GET 하나로 Workspace VM을 포함한 모든 target을 얻는다. Workspace VM 하나만 돌려주는 구현은 실패다.
func TestTerminalTargetsReturnsEveryVMOfTheSnapshot(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture

	list := e.decodeTargets(e.request(http.MethodGet, e.targetsPath(f.LabInstanceID.String()), e.ownerCookie, ""))
	if list.Generation != 1 || list.WorkspaceVMKey != "workspace" {
		t.Fatalf("list = %+v, want generation 1 workspace", list)
	}
	want := terminaltest.DefaultVMs()
	if len(list.Items) != len(want) || len(list.Items) < 2 {
		t.Fatalf("items = %+v, want all %d snapshot VMs", list.Items, len(want))
	}
	for i, vm := range want {
		if got := list.Items[i]; got.VMKey != vm.Key || got.Role != vm.Role || got.InstanceIndex != int64(vm.Index) {
			t.Fatalf("items[%d] = %+v, want %+v", i, got, vm)
		}
	}
	// workspaceVmKey는 items 중 하나이며 유일한 target이 아니다.
	list.item(t, list.WorkspaceVMKey)

	// 조회는 side effect가 없다. Connector나 Relay를 쓰지 않는다.
	e.requireNoSideEffects()
}

// Provider topology, Connector, resolve된 이미지·flavor, 주소, snapshot 원문은 Browser 응답에 없다.
func TestTerminalTargetsExposeNoProviderTopology(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture

	resp := e.request(http.MethodGet, e.targetsPath(f.LabInstanceID.String()), e.ownerCookie, "")
	e.decodeTargets(resp)
	body := string(resp.Body)

	forbidden := []string{
		// 실제 값
		f.ConnectorID.String(), f.ProviderConnectionID.String(), f.OrganizationID.String(), f.LabExecutionID.String(), f.LabSpecID.String(),
		f.OwnerID.String(), f.PeerLabInstanceID.String(), f.PeerServer.ProviderID, f.PeerServer.ResourceID.String(),
		// snapshot에만 있는 resolve된 값
		terminaltest.SnapshotImageIDMarker, terminaltest.SnapshotFlavorIDMarker, terminaltest.SnapshotPrivateIPMarker,
		terminaltest.SnapshotStartupMarker, terminaltest.SnapshotProviderConnMark, terminaltest.Credential,
	}
	for name, server := range f.Servers {
		forbidden = append(forbidden, server.ProviderID, server.ResourceID.String(), "provider-"+name)
	}
	for _, value := range forbidden {
		if strings.Contains(body, value) {
			t.Fatalf("응답이 %q를 포함함: %s", value, body)
		}
	}
	// field 이름도 없다.
	lower := strings.ToLower(body)
	for _, field := range []string{
		"providerserverid", "providerresourceid", "connectorid", "providerconnectionid", "privateip", "imageid", "flavorid", "flavorspec",
		"startupscript", "internetoutbound", "ssh", "credential", "password", "managementnetwork", "creationsnapshot", "organizationid",
	} {
		if strings.Contains(lower, field) {
			t.Fatalf("응답이 %q field를 포함함: %s", field, body)
		}
	}
}

// 목록은 해당 LabExecution에 고정된 snapshot이다. 이후 mutable LabSpec이 바뀌어도 다시 해석하지 않는다.
func TestTerminalTargetsIgnoreLaterLabSpecChanges(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture
	path := e.targetsPath(f.LabInstanceID.String())

	before := e.decodeTargets(e.request(http.MethodGet, path, e.ownerCookie, ""))
	terminaltest.Exec(t, e.conn, `UPDATE lab_specs SET workspace_role = 'changed-role', workspace_instance_index = 7, name = 'Renamed', revision = revision + 1 WHERE id = $1`, f.LabSpecID)
	after := e.decodeTargets(e.request(http.MethodGet, path, e.ownerCookie, ""))

	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("LabSpec 변경 뒤 목록이 바뀜:\nbefore %+v\nafter  %+v", before, after)
	}
}

// generation은 판정 시점의 LabInstance generation이다. Reset 뒤에는 새 값이며 target 목록(logical)은 같은 snapshot이다.
func TestTerminalTargetsReportTheCurrentGeneration(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture
	path := e.targetsPath(f.LabInstanceID.String())

	before := e.decodeTargets(e.request(http.MethodGet, path, e.ownerCookie, ""))
	generation, _ := f.BumpGeneration(t, e.conn, f.LabInstanceID)
	after := e.decodeTargets(e.request(http.MethodGet, path, e.ownerCookie, ""))

	if before.Generation != 1 || after.Generation != generation || generation != 2 {
		t.Fatalf("generation before/after = %d/%d, want 1/2", before.Generation, after.Generation)
	}
	if fmt.Sprint(before.Items) != fmt.Sprint(after.Items) || before.WorkspaceVMKey != after.WorkspaceVMKey {
		t.Fatalf("Reset 뒤 target 목록이 바뀜: %+v / %+v", before, after)
	}
}

// 조회도 TerminalSession 생성과 같은 권한 경계를 쓴다. 거절된 요청은 어떤 side effect도 만들지 않는다.
func TestTerminalTargetsAreDeniedLikeSessionCreation(t *testing.T) {
	type testCase struct {
		name       string
		cookie     func(*terminalEnv) string
		labID      func(*terminalEnv) string
		setup      func(*terminalEnv)
		wantStatus int
		wantCode   string
	}
	owner := func(e *terminalEnv) string { return e.ownerCookie }
	ownLab := func(e *terminalEnv) string { return e.fixture.LabInstanceID.String() }
	status := func(s string) func(*terminalEnv) {
		return func(e *terminalEnv) {
			terminaltest.Exec(t, e.conn, `UPDATE lab_instances SET status = $2 WHERE id = $1`, e.fixture.LabInstanceID, s)
		}
	}
	// Organization ADMIN이어도 LabInstance 소유나 ClassMembership을 대체하지 못한다.
	admin := func(e *terminalEnv) string {
		id := uuid.New()
		terminaltest.Exec(t, e.conn, `INSERT INTO users (id, organization_id, organization_role) VALUES ($1, $2, 'ADMIN')`, id, e.fixture.OrganizationID)
		return terminaltest.LoginSession(t, e.conn, id)
	}

	tests := []testCase{
		{name: "no login session", cookie: func(*terminalEnv) string { return "" }, labID: ownLab, wantStatus: 401, wantCode: "unauthenticated"},
		{name: "unknown login session", cookie: func(*terminalEnv) string { return "unknown-session" }, labID: ownLab, wantStatus: 401, wantCode: "unauthenticated"},
		{name: "owner reads another student's lab instance", cookie: owner, labID: func(e *terminalEnv) string { return e.fixture.PeerLabInstanceID.String() }, wantStatus: 403, wantCode: "forbidden"},
		{name: "peer reads the owner's lab instance", cookie: func(e *terminalEnv) string { return e.peerCookie }, labID: ownLab, wantStatus: 403, wantCode: "forbidden"},
		{
			name:   "instructor of the class reads a student's lab instance",
			cookie: func(e *terminalEnv) string { return terminaltest.LoginSession(t, e.conn, e.fixture.InstructorID) },
			labID:  ownLab, wantStatus: 403, wantCode: "forbidden",
		},
		{name: "organization admin without ownership", cookie: admin, labID: ownLab, wantStatus: 403, wantCode: "forbidden"},
		{
			name: "organization admin who instructs the class", labID: ownLab, wantStatus: 403, wantCode: "forbidden",
			setup: func(e *terminalEnv) {
				terminaltest.Exec(t, e.conn, `UPDATE users SET organization_role = 'ADMIN' WHERE id = $1`, e.fixture.InstructorID)
			},
			cookie: func(e *terminalEnv) string { return terminaltest.LoginSession(t, e.conn, e.fixture.InstructorID) },
		},
		{name: "user of another organization", cookie: func(e *terminalEnv) string { return e.foreignCookie }, labID: ownLab, wantStatus: 403, wantCode: "forbidden"},
		{name: "user without membership in the class", cookie: func(e *terminalEnv) string { return e.outsiderCookie }, labID: ownLab, wantStatus: 403, wantCode: "forbidden"},
		{
			name: "owner whose class membership was removed", cookie: owner, labID: ownLab, wantStatus: 403, wantCode: "forbidden",
			setup: func(e *terminalEnv) {
				terminaltest.Exec(t, e.conn, `DELETE FROM class_memberships WHERE class_id = $1 AND user_id = $2`, e.fixture.ClassID, e.fixture.OwnerID)
			},
		},
		{name: "unknown lab instance", cookie: owner, labID: func(*terminalEnv) string { return uuid.NewString() }, wantStatus: 404, wantCode: "not_found"},
		{name: "malformed lab instance id", cookie: owner, labID: func(*terminalEnv) string { return "not-a-uuid" }, wantStatus: 404, wantCode: "not_found"},
		{name: "non-canonical lab instance id", cookie: owner, labID: func(e *terminalEnv) string { return strings.ToUpper(e.fixture.LabInstanceID.String()) }, wantStatus: 404, wantCode: "not_found"},
		{name: "lab instance PENDING", cookie: owner, labID: ownLab, setup: status("PENDING"), wantStatus: 409, wantCode: "lab_instance_not_ready"},
		{name: "lab instance PROVISIONING", cookie: owner, labID: ownLab, setup: status("PROVISIONING"), wantStatus: 409, wantCode: "lab_instance_not_ready"},
		{name: "lab instance ERROR", cookie: owner, labID: ownLab, setup: status("ERROR"), wantStatus: 409, wantCode: "lab_instance_not_ready"},
		{name: "lab instance DELETING", cookie: owner, labID: ownLab, setup: status("DELETING"), wantStatus: 409, wantCode: "lab_instance_not_ready"},
		{name: "lab instance in an unknown future state", cookie: owner, labID: ownLab, setup: status("SOMETHING_NEW"), wantStatus: 409, wantCode: "lab_instance_not_ready"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTerminalEnv(t)
			if tt.setup != nil {
				tt.setup(e)
			}
			resp := e.request(http.MethodGet, e.targetsPath(tt.labID(e)), tt.cookie(e), "")
			if resp.Status != tt.wantStatus || e.problemCode(resp) != tt.wantCode {
				t.Fatalf("응답 = %d %s, want %d %s", resp.Status, resp.Body, tt.wantStatus, tt.wantCode)
			}
			if resp.Header.Get("Content-Type") != "application/problem+json" || resp.Header.Get("Cache-Control") != "no-store" {
				t.Fatalf("Content-Type = %q, Cache-Control = %q", resp.Header.Get("Content-Type"), resp.Header.Get("Cache-Control"))
			}
			// 거절 응답에 target이나 내부 정보가 없다.
			lower := strings.ToLower(string(resp.Body))
			for _, leaked := range []string{"vmkey", "workspace", "sqlstate", terminaltest.SnapshotImageIDMarker} {
				if strings.Contains(lower, leaked) {
					t.Fatalf("거절 응답이 %q를 포함함: %s", leaked, resp.Body)
				}
			}
			e.requireNoSideEffects()
		})
	}
}

// 저장된 CreationSnapshot을 신뢰하지 않는다. 손상되었으면 일부만 반환하거나 값을 만들어 채우지 않고 500으로 닫는다.
// TerminalSession 생성도 같은 snapshot 판정을 쓰므로 같은 이유로 거절하고 side effect를 만들지 않는다.
func TestTerminalTargetsFailClosedOnCorruptSnapshots(t *testing.T) {
	vm := func(key, role string, index any) map[string]any {
		return map[string]any{"vmKey": key, "role": role, "instanceIndex": index, "imageId": terminaltest.SnapshotImageIDMarker}
	}
	snapshot := func(workspace any, vms any) string {
		raw, err := json.Marshal(map[string]any{"providerConnectionId": "pc", "vms": vms, "workspaceVmKey": workspace, "internetOutbound": false})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	good := []any{vm("w", "web", 0), vm("x", "worker", 0)}

	tests := map[string]string{
		"empty snapshot object":          `{}`,
		"no vms":                         snapshot("w", nil),
		"empty vms":                      snapshot("w", []any{}),
		"vms is not an array":            snapshot("w", "oops"),
		"no workspaceVmKey":              `{"vms":[{"vmKey":"w","role":"web","instanceIndex":0}]}`,
		"empty workspaceVmKey":           snapshot("", good),
		"workspaceVmKey is not a string": snapshot(7, good),
		"workspaceVmKey is not a vm":     snapshot("missing", good),
		"empty vmKey":                    snapshot("w", []any{vm("w", "web", 0), vm("", "worker", 0)}),
		"empty role":                     snapshot("w", []any{vm("w", "web", 0), vm("x", "", 0)}),
		"negative instanceIndex":         snapshot("w", []any{vm("w", "web", 0), vm("x", "worker", -1)}),
		"missing instanceIndex":          `{"vms":[{"vmKey":"w","role":"web"}],"workspaceVmKey":"w"}`,
		"instanceIndex is a string":      snapshot("w", []any{vm("w", "web", "0")}),
		"duplicate vmKey":                snapshot("w", []any{vm("w", "web", 0), vm("w", "worker", 1)}),
	}
	names := make([]string, 0, len(tests))
	for name := range tests {
		names = append(names, name)
	}
	sort.Strings(names)

	e := newTerminalEnv(t)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			lab := e.fixture.AddLabInstanceWithSnapshot(t, e.conn, tests[name])
			// 생성 판정이 snapshot membership보다 ProviderResource를 먼저 보지 않음을 확인하려고 PRESENT SERVER를 둔다.
			e.fixture.AddResource(t, e.conn, lab, 1, "SERVER", "w", "PRESENT")

			resp := e.request(http.MethodGet, e.targetsPath(lab.String()), e.ownerCookie, "")
			if resp.Status != http.StatusInternalServerError || e.problemCode(resp) != "internal_error" {
				t.Fatalf("GET = %d %s, want 500 internal_error", resp.Status, resp.Body)
			}
			create := e.request(http.MethodPost, e.createPath(lab), e.ownerCookie, createBodyFor("w"))
			if create.Status != http.StatusInternalServerError || e.problemCode(create) != "internal_error" {
				t.Fatalf("POST = %d %s, want 500 internal_error", create.Status, create.Body)
			}
			for _, r := range []apiResponse{resp, create} {
				// 응답에 snapshot의 값이나 내부 오류 문구가 없다.
				for _, leaked := range []string{terminaltest.SnapshotImageIDMarker, "vmKey", "instanceIndex", "snapshot", "json", "중복", "sqlstate"} {
					if strings.Contains(string(r.Body), leaked) {
						t.Fatalf("응답이 %q를 포함함: %s", leaked, r.Body)
					}
				}
			}
			e.requireNoSideEffects()
		})
	}

	t.Run("lab instance without any creation snapshot", func(t *testing.T) {
		execution, lab := uuid.New(), uuid.New()
		f := e.fixture
		terminaltest.Exec(t, e.conn, `INSERT INTO lab_executions (id, organization_id, class_id, lab_spec_id, instructor_user_id, status, finished_at)
			VALUES ($1, $2, $3, $4, $5, 'FINISHED', now())`, execution, f.OrganizationID, f.ClassID, f.LabSpecID, f.InstructorID)
		terminaltest.Exec(t, e.conn, `INSERT INTO lab_instances (id, organization_id, lab_execution_id, user_id, participant_role, status, generation)
			VALUES ($1, $2, $3, $4, 'STUDENT', 'READY', 1)`, lab, f.OrganizationID, execution, f.OwnerID)

		// LabInstance는 있으므로 404가 아니다. 저장된 관계가 모순이므로 500이다.
		resp := e.request(http.MethodGet, e.targetsPath(lab.String()), e.ownerCookie, "")
		if resp.Status != http.StatusInternalServerError || e.problemCode(resp) != "internal_error" {
			t.Fatalf("GET = %d %s, want 500 internal_error", resp.Status, resp.Body)
		}
		e.requireNoSideEffects()
	})
}

// 이번 follow-up의 핵심 Acceptance다. Browser가 undocumented convention 없이 공식 응답만 사용해 자신의 READY LabInstance에서
// Terminal을 열 수 있는 VM의 opaque vmKey를 얻고, 그 값 그대로 TerminalSession을 만든다. Workspace VM 외 VM도 동일하다.
//
//	GET terminal-targets → 선택한 vmKey 그대로 → POST terminal-sessions → TERMINAL_OPEN → Data attach → 201 → Browser attach
//
// vmKey는 role/instanceIndex와 무관한 opaque 값이라, 생성 규칙을 추측하는 클라이언트는 이 test에서 실패한다.
func TestBrowserDiscoversTargetsAndCreatesSessionsForEveryVM(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture

	vms := []terminaltest.VM{
		{Key: "vk-9d41a7e0", Role: "web", Index: 0},
		{Key: "vk-b27e5c13", Role: "worker", Index: 0},
		{Key: "vk-c803f1d9", Role: "worker", Index: 1},
	}
	lab := f.AddLabInstanceWithSnapshot(t, e.conn, terminaltest.Snapshot("vk-9d41a7e0", vms))
	servers := map[string]terminaltest.Server{}
	for _, vm := range vms {
		servers[vm.Key] = f.AddResource(t, e.conn, lab, 1, "SERVER", vm.Key, "PRESENT")
	}
	// 현재 generation에 PRESENT SERVER ProviderResource가 있지만 CreationSnapshot에는 없는 이름이다. Terminal target이 아니다.
	f.AddResource(t, e.conn, lab, 1, "SERVER", "hidden-server", "PRESENT")
	// 하드코딩하거나 role/index로 만들어 낸 key. 같은 이름의 PRESENT 리소스가 있어도 target이 아니다.
	for _, guess := range []string{"workspace", "worker-1", "worker:1", "worker_1", "web-0"} {
		f.AddResource(t, e.conn, lab, 1, "SERVER", guess, "PRESENT")
	}

	// 1. 공식 GET으로 target 목록을 얻는다. snapshot에 없는 리소스는 보이지 않는다.
	list := e.decodeTargets(e.request(http.MethodGet, e.targetsPath(lab.String()), e.ownerCookie, ""))
	if list.Generation != 1 || list.WorkspaceVMKey != "vk-9d41a7e0" || len(list.Items) != 3 {
		t.Fatalf("list = %+v", list)
	}
	for _, item := range list.Items {
		if item.VMKey == "hidden-server" || item.VMKey == "workspace" {
			t.Fatalf("snapshot에 없는 VM이 목록에 있음: %+v", list.Items)
		}
	}

	// 2. snapshot에 없는 key는 같은 이름의 PRESENT SERVER가 있어도 만들 수 없다(422 invalid_terminal_target).
	for _, hidden := range []string{"hidden-server", "workspace", "worker-1", "worker:1", "worker_1", "web-0"} {
		resp := e.request(http.MethodPost, e.createPath(lab), e.ownerCookie, createBodyFor(hidden))
		if resp.Status != http.StatusUnprocessableEntity || e.problemCode(resp) != "invalid_terminal_target" {
			t.Fatalf("targetVmKey %q = %d %s, want 422 invalid_terminal_target", hidden, resp.Status, resp.Body)
		}
	}
	e.requireNoSideEffects()

	// 3. GET이 돌려준 값 그대로 보낸다. Workspace VM이 아닌 VM(role worker, index 1)을 고른다. 키를 하드코딩하지 않는다.
	var picked targetItem
	for _, item := range list.Items {
		if item.Role == "worker" && item.InstanceIndex == 1 {
			picked = item
		}
	}
	if picked.VMKey == "" || picked.VMKey == list.WorkspaceVMKey {
		t.Fatalf("worker-1을 목록에서 찾지 못함: %+v", list.Items)
	}
	s := e.createSessionFor(e.ownerCookie, lab, picked.VMKey)

	// 4. 서버는 그 key를 immutable snapshot과 현재 generation의 ProviderResource로 다시 resolve했다.
	opens := e.connector.Opens()
	if len(opens) != 1 {
		t.Fatalf("TERMINAL_OPEN = %d, want 1", len(opens))
	}
	open := opens[0]
	if open.TerminalSessionID != s.ID || open.LabInstanceID != lab.String() || open.Generation != 1 ||
		open.TargetVMKey != picked.VMKey || open.ProviderServerID != servers[picked.VMKey].ProviderID {
		t.Fatalf("TERMINAL_OPEN = %+v, want target %q on server %q", open, picked.VMKey, servers[picked.VMKey].ProviderID)
	}
	if got := e.connector.Attached(s.ID); len(got) != 1 {
		t.Fatalf("Terminal Data attach = %v, want 1", got)
	}
	e.resourceOf(t, s.ID, servers[picked.VMKey].ResourceID)
	if _, attached := e.attach(s); attached["type"] != "TERMINAL_ATTACHED" {
		t.Fatalf("Browser attach = %v", attached)
	}

	// 5. 목록의 나머지 VM(Workspace VM 포함)도 같은 방식으로 TerminalSession을 만들 수 있다. Workspace VM으로 강제하지 않는다.
	for _, item := range list.Items {
		if item.VMKey == picked.VMKey {
			continue
		}
		created := e.createSessionFor(e.ownerCookie, lab, item.VMKey)
		e.resourceOf(t, created.ID, servers[item.VMKey].ResourceID)
	}
	if n := e.sessionCount(); n != 3 {
		t.Fatalf("terminal_sessions = %d, want 3", n)
	}
}

// 조회 뒤 Reset이 일어나도 조회 결과를 생성의 근거로 쓰지 않는다. 생성은 현재 generation의 ProviderResource를 다시 확인한다.
func TestSessionCreationRevalidatesTheTargetAgainstTheCurrentGeneration(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture

	list := e.decodeTargets(e.request(http.MethodGet, e.targetsPath(f.LabInstanceID.String()), e.ownerCookie, ""))
	db := list.item(t, "db")

	// Reset으로 generation이 올랐지만 새 generation에 db VM이 아직 없다. snapshot에는 있는 VM이므로 잘못된 입력(422)이 아니라
	// 지금 사용할 수 없는 target(409)이다.
	generation, _ := f.BumpGeneration(t, e.conn, f.LabInstanceID)
	resp := e.request(http.MethodPost, e.createPath(f.LabInstanceID), e.ownerCookie, createBodyFor(db.VMKey))
	if resp.Status != http.StatusConflict || e.problemCode(resp) != "terminal_target_unavailable" {
		t.Fatalf("POST = %d %s, want 409 terminal_target_unavailable", resp.Status, resp.Body)
	}
	e.requireNoSideEffects()

	// 새 generation에 resource가 생기면 같은 key로 만들 수 있고 Connector는 새 generation과 새 Provider Server를 받는다.
	next := f.AddResource(t, e.conn, f.LabInstanceID, generation, "SERVER", db.VMKey, "PRESENT")
	s := e.createSessionFor(e.ownerCookie, f.LabInstanceID, db.VMKey)
	open := e.connector.Opens()[0]
	if s.Generation != generation || open.Generation != generation || open.ProviderServerID != next.ProviderID || open.ProviderServerID == f.Servers["db"].ProviderID {
		t.Fatalf("TERMINAL_OPEN = %+v, session generation %d, want generation %d on the new server", open, s.Generation, generation)
	}
}

func (e *terminalEnv) createSessionFor(cookie string, labInstanceID uuid.UUID, vmKey string) created {
	e.t.Helper()
	resp := e.request(http.MethodPost, e.createPath(labInstanceID), cookie, createBodyFor(vmKey))
	if resp.Status != http.StatusCreated {
		e.t.Fatalf("TerminalSession 생성(%q) = %d, want 201: %s", vmKey, resp.Status, resp.Body)
	}
	body := resp.json(e.t)
	return created{
		ID: body["id"].(string), Token: body["sessionToken"].(string), TokenExpiresAt: body["tokenExpiresAt"].(string),
		Generation: int64(body["generation"].(float64)), cookie: cookie,
	}
}

// resourceOf는 TerminalSession이 가리키는 ProviderResource가 want인지 확인한다.
func (e *terminalEnv) resourceOf(t *testing.T, sessionID string, want uuid.UUID) {
	t.Helper()
	var got uuid.UUID
	if err := e.conn.QueryRow(t.Context(), `SELECT provider_resource_id FROM terminal_sessions WHERE id = $1`, sessionID).Scan(&got); err != nil || got != want {
		t.Fatalf("provider_resource_id = %v, %v, want %v", got, err, want)
	}
}
