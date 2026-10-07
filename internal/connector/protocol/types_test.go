package protocol

import (
	"encoding/json"
	"testing"
)

// 이 test는 connector.schema.json이 허용하는 값을 shared type이 JSON tag 때문에 잃지 않는지 raw JSON으로 확인한다.
// 구조체 decode만으로는 property가 사라졌는지 null로 나갔는지 구분할 수 없으므로 member를 직접 본다.

func members(t *testing.T, v any) map[string]json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("object가 아님: %s", data)
	}
	return out
}

func payloadMembers(t *testing.T, v any) map[string]json.RawMessage {
	t.Helper()
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(members(t, v)["payload"], &payload); err != nil {
		t.Fatalf("payload가 object가 아님: %v", err)
	}
	return payload
}

func requireMember(t *testing.T, m map[string]json.RawMessage, name, want string) {
	t.Helper()
	got, present := m[name]
	if !present {
		t.Fatalf("%s property가 wire에서 사라짐", name)
	}
	if string(got) != want {
		t.Fatalf("%s = %s, want %s", name, got, want)
	}
}

// providerResources는 mutationType마다 요구가 다르다(connector.schema.json).
//   - PROVISION: property 없음
//   - RESET: 비어 있지 않은 목록 필수(각 항목에 logicalName)
//   - CLEANUP: property 필수이며 array에 minItems가 없으므로 빈 목록 "[]"도 유효
//
// 이 type은 요구를 검사하지 않고 직렬화만 하므로, 여기서는 각 mutation의 유효한 값이 wire에서 사라지거나 바뀌지 않는지와
// CLEANUP 때문에 필요한 nil(누락) / 빈 목록([]) 구분이 유지되는지 확인한다. nil은 property를 만들지 않고 null도 만들지 않는다.
func TestOperationCommandProviderResourcesWireEncoding(t *testing.T) {
	cmd := func(p OperationCommandPayload) OperationCommandMessage { return OperationCommandMessage{Payload: p} }

	t.Run("provision has no providerResources property", func(t *testing.T) {
		got := payloadMembers(t, cmd(OperationCommandPayload{MutationType: MutationTypeProvision}))
		if raw, present := got["providerResources"]; present {
			t.Fatalf("PROVISION의 nil ProviderResources가 wire에 %s로 나감", raw)
		}
		requireMember(t, got, "mutationType", `"PROVISION"`)
	})
	t.Run("reset serializes the non-empty previous-generation list with logicalName", func(t *testing.T) {
		got := payloadMembers(t, cmd(OperationCommandPayload{MutationType: MutationTypeReset, ProviderResources: []ProviderResourceRef{
			{ResourceType: "SERVER", ProviderID: "srv-old", Generation: 1, LogicalName: "vm-1"},
			{ResourceType: "NETWORK", ProviderID: "net-old", Generation: 1, LogicalName: "lab-net"},
		}}))
		requireMember(t, got, "providerResources", `[{"resourceType":"SERVER","providerId":"srv-old","generation":1,"logicalName":"vm-1"},{"resourceType":"NETWORK","providerId":"net-old","generation":1,"logicalName":"lab-net"}]`)
		requireMember(t, got, "mutationType", `"RESET"`)
	})
	t.Run("cleanup with explicit empty list keeps the required property as an empty array", func(t *testing.T) {
		got := payloadMembers(t, cmd(OperationCommandPayload{MutationType: MutationTypeCleanup, ProviderResources: []ProviderResourceRef{}}))
		requireMember(t, got, "providerResources", "[]")
		requireMember(t, got, "mutationType", `"CLEANUP"`)
	})
	t.Run("cleanup with resources", func(t *testing.T) {
		got := payloadMembers(t, cmd(OperationCommandPayload{MutationType: MutationTypeCleanup, ProviderResources: []ProviderResourceRef{{ResourceType: "SERVER", ProviderID: "srv-1", Generation: 1}}}))
		requireMember(t, got, "providerResources", `[{"resourceType":"SERVER","providerId":"srv-1","generation":1}]`)
	})
	t.Run("cleanup missing list stays missing, never null", func(t *testing.T) {
		got := payloadMembers(t, cmd(OperationCommandPayload{MutationType: MutationTypeCleanup}))
		if raw, present := got["providerResources"]; present {
			t.Fatalf("CLEANUP의 nil ProviderResources가 wire에 %s로 나감", raw)
		}
	})
	t.Run("marshaling a pointer behaves the same", func(t *testing.T) {
		message := cmd(OperationCommandPayload{MutationType: MutationTypeCleanup, ProviderResources: []ProviderResourceRef{}})
		requireMember(t, payloadMembers(t, &message), "providerResources", "[]")
	})
	t.Run("other payload members are unchanged", func(t *testing.T) {
		snapshot := &CreationSnapshot{ProviderConnectionID: "pc-1", WorkspaceVMKey: "vm-1"}
		got := payloadMembers(t, cmd(OperationCommandPayload{MutationType: MutationTypeProvision, CreationSnapshot: snapshot}))
		if _, present := got["creationSnapshot"]; !present {
			t.Fatal("creationSnapshot이 사라짐")
		}
		if _, present := got["creationSnapshot"]; present && len(got) != 2 {
			t.Fatalf("payload member = %v, want mutationType과 creationSnapshot뿐", got)
		}
	})
}

// wire를 struct로 읽을 때 빈 배열([])은 빈 non-nil, 누락과 null은 nil이다. 소비자가 "명시적 빈 목록"과 "누락"을 구분할 수 있다.
func TestOperationCommandProviderResourcesDecodeKeepsMissingAndEmptyApart(t *testing.T) {
	decode := func(payload string) OperationCommandPayload {
		t.Helper()
		var p OperationCommandPayload
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if got := decode(`{"mutationType":"CLEANUP","providerResources":[]}`); got.ProviderResources == nil || len(got.ProviderResources) != 0 {
		t.Fatalf("[] = %#v, want 빈 non-nil", got.ProviderResources)
	}
	if got := decode(`{"mutationType":"CLEANUP"}`); got.ProviderResources != nil {
		t.Fatalf("누락 = %#v, want nil", got.ProviderResources)
	}
	if got := decode(`{"mutationType":"CLEANUP","providerResources":null}`); got.ProviderResources != nil {
		t.Fatalf("null = %#v, want nil", got.ProviderResources)
	}
	reset := decode(`{"mutationType":"RESET","providerResources":[{"resourceType":"SERVER","providerId":"srv-old","generation":1,"logicalName":"vm-1"}]}`)
	if len(reset.ProviderResources) != 1 || reset.ProviderResources[0] != (ProviderResourceRef{ResourceType: "SERVER", ProviderID: "srv-old", Generation: 1, LogicalName: "vm-1"}) {
		t.Fatalf("RESET decode = %#v", reset.ProviderResources)
	}
}

// Schema가 허용하는 0/false/빈 문자열/빈 배열 값이 wire에서 사라지지 않는다.
func TestSchemaValidZeroValuesSurviveSerialization(t *testing.T) {
	t.Run("creationSnapshot", func(t *testing.T) {
		snapshot := CreationSnapshot{
			ProviderConnectionID: "pc-1",
			VMs: []ResolvedVmSpec{{
				VMKey: "vm-1", Role: "workspace", InstanceIndex: 0, ImageID: "img-1", FlavorID: "fl-1",
				FlavorSpec: &ResolvedFlavorSpec{VCPUs: 1, RAMMiB: 1, DiskGiB: 0},
			}},
			WorkspaceVMKey:   "vm-1",
			InternetOutbound: false,
			StartupScript:    &StartupScriptSnapshot{Content: "", SHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		}
		got := members(t, snapshot)
		requireMember(t, got, "internetOutbound", "false")

		var script map[string]json.RawMessage
		if err := json.Unmarshal(got["startupScript"], &script); err != nil {
			t.Fatalf("startupScript가 wire에서 사라짐: %v", err)
		}
		requireMember(t, script, "content", `""`)
		requireMember(t, script, "sha256", `"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"`)

		var vms []map[string]json.RawMessage
		if err := json.Unmarshal(got["vms"], &vms); err != nil || len(vms) != 1 {
			t.Fatalf("vms = %s", got["vms"])
		}
		requireMember(t, vms[0], "instanceIndex", "0")
		var flavor map[string]json.RawMessage
		if err := json.Unmarshal(vms[0]["flavorSpec"], &flavor); err != nil {
			t.Fatal(err)
		}
		requireMember(t, flavor, "diskGiB", "0")
	})

	t.Run("startupScript absent when not set", func(t *testing.T) {
		if _, present := members(t, CreationSnapshot{ProviderConnectionID: "pc-1"})["startupScript"]; present {
			t.Fatal("optional startupScript가 nil인데 wire에 실림")
		}
	})

	t.Run("operation result keeps the required empty array", func(t *testing.T) {
		requireMember(t, payloadMembers(t, OperationResultMessage{Payload: OperationResultPayload{Outcome: OutcomeSucceeded, ProviderResources: []ProviderResourceResult{}}}), "providerResources", "[]")
	})
	t.Run("reconcile result keeps the required empty array", func(t *testing.T) {
		requireMember(t, payloadMembers(t, ReconcileResultMessage{Payload: ReconcileResultPayload{Observations: []ResourceObservation{}}}), "observations", "[]")
	})
	t.Run("reconcile request keeps the required empty array", func(t *testing.T) {
		requireMember(t, payloadMembers(t, ReconcileRequestMessage{Payload: ReconcileRequestPayload{KnownResources: []ProviderResourceRef{}}}), "knownResources", "[]")
	})
	t.Run("required booleans keep false", func(t *testing.T) {
		requireMember(t, payloadMembers(t, OperationAckMessage{Payload: OperationAckPayload{Accepted: false}}), "accepted", "false")
		var observations []map[string]json.RawMessage
		raw := payloadMembers(t, ReconcileResultMessage{Payload: ReconcileResultPayload{Observations: []ResourceObservation{{ResourceType: "SERVER", ProviderID: "srv-1", Exists: false, Source: "KNOWN_RESOURCE"}}}})
		if err := json.Unmarshal(raw["observations"], &observations); err != nil || len(observations) != 1 {
			t.Fatalf("observations = %s", raw["observations"])
		}
		requireMember(t, observations[0], "exists", "false")
	})
	t.Run("progress stage", func(t *testing.T) {
		requireMember(t, payloadMembers(t, OperationProgressMessage{Payload: OperationProgressPayload{Stage: "CREATE_VM"}}), "stage", `"CREATE_VM"`)
	})
}
