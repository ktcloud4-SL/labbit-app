"""Summarize already-written local artifacts; no cloud/service access."""
import hashlib
import ipaddress
import json
import re
from pathlib import Path

OUT = Path(__file__).resolve().parent
RAW = OUT.parent / "lbt174-routing-postadmin-20261016"
provenance = []

def read(path):
    raw = (RAW / path).read_bytes()
    provenance.append({"path": path, "sha256": hashlib.sha256(raw).hexdigest(), "bytes": len(raw)})
    return raw.decode("utf-8-sig")

def lines(path):
    return [json.loads(line) for line in read(path).splitlines() if line.startswith("{")]

def result(path):
    return json.loads(read(path))

def probe_summary(probe):
    return {"connectCode": probe["connectCode"], "elapsedMs": probe["elapsed_ms"],
            "sourceRole": probe["socketSourceRole"],
            "tupleRoles": [r["role"] for r in probe["socketTupleRoute"]],
            "defaults": probe["defaultRoutes"]}

first = [probe_summary(lines(f"internet-first-{i:02d}.log")[0]) for i in range(1, 13)]
assert all(p["connectCode"] == 0 and p["sourceRole"] == "lab" and p["tupleRoles"] == ["lab"] for p in first)
assert all(sorted(d["role"] for d in p["defaults"]) == ["lab", "management"] for p in first)
after = probe_summary(lines("after-timeout.log")[0])
strict = result("probe-run-result.json")
assert strict["exitCode"] == 1
assert strict["sourceBefore"] == strict["sourceAfter"]
strict_events = lines("probe-live.log")
strict_outputs = "".join(event.get("Output", "") for event in strict_events)
cleanup_phrase = "exact tracked absence checked: resources=8"
sg_phrase = "Temporary Management security group GET absence confirmed"
key_phrase = "Temporary integration keypair GET absence confirmed"
assert all(phrase in strict_outputs for phrase in (cleanup_phrase, sg_phrase, key_phrase))
assert "phase=diagnostic-timeout-cleanup exit=0 elapsed_ms=1442 joined=true" in strict_outputs
agents = []
for event in lines("backend-observer.log"):
    match = re.search(r'networkAgent type="([^"]+)" binary="([^"]+)" alive=(true|false) adminStateUp=(true|false)', event.get("Output", ""))
    if match:
        agents.append({"type": match[1], "binary": match[2], "alive": match[3] == "true", "adminStateUp": match[4] == "true"})
lease_raw = read("lease-observer/dhcp-lease-origin.log")
lease_entries = [json.loads(line) for line in lease_raw.splitlines() if line.startswith("{")]
management = next(entry for entry in lease_entries if entry["role"] == "management")
assert management["serverIsNeutronDHCPPort"] is True
assert "ROUTER" in management["lease"] and "ROUTES" not in management["lease"]
assert "TypeError: Object of type bytes is not JSON serializable" in lease_raw
lease_events = lines("lease-observer/probe-live.log")
lease_outputs = "".join(event.get("Output", "") for event in lease_events)
assert all(phrase in lease_outputs for phrase in (cleanup_phrase, sg_phrase, key_phrase))
assert '"opt_value": ""' not in lease_outputs  # output uses compact JSON
assert '\\"opt_value\\":\\"\\"' not in lease_outputs  # JSONL was decoded already
opts_text = lease_outputs.split("owned Management DHCP options=", 1)[1].split("\n", 1)[0]
opts = json.loads(opts_text)
assert len(opts) == 2 and {opt["opt_name"] for opt in opts} == {"router", "classless-static-route"}
assert all(opt["opt_value"] == "" and opt["ip_version"] == 4 for opt in opts)
report = {
    "readOnly": True,
    "actualSourceIdentity": strict["sourceAfter"],
    "strictProbe": {"childExitCode": strict["exitCode"], "elapsedSeconds": strict["elapsedSeconds"],
                    "firstCalls": first, "afterTimeout": after,
                    "intentionalTimeout": {"exitCode": 0, "elapsedMs": 1442, "joined": True},
                    "trackedAbsence": 8, "securityGroupGETAbsence": True, "keypairGETAbsence": True,
                    "interpretation": "TCP and Lab source passed; sole-Lab-default routing requirement failed. Later lifecycle gates not reached."},
    "agents": agents,
    "leaseObserver": {"managementServerMatchesQueriedNeutronDHCPPort": True,
                      "managementLeaseHasRouter": True, "managementLeaseHasRoutesField": False,
                      "canonicalEmptyAPIOptions": opts,
                      "wholeTestPass": False, "netplanObserved": False,
                      "failure": "TypeError serializing bytes after the lease observations",
                      "trackedAbsence": 8, "securityGroupGETAbsence": True, "keypairGETAbsence": True},
    "limits": ["No private IP/MAC or credentials are emitted in this summary.",
               "No DHCP packet capture or installed daemon version/config was inspected.",
               "Lease field absence is an observed file fact, not proof an option was absent on wire."],
    "rawProvenance": provenance,
}
candidate = result("candidate-onlink-v2/probe-run-result.json")
candidate_events = lines("candidate-onlink-v2/probe-live.log")
candidate_outputs = "".join(event.get("Output", "") for event in candidate_events)
assert "candidate policy exact owned-port GET readback PASS" in candidate_outputs
candidate_renew = lines("candidate-onlink-v2/candidate-renew-management-first.log")
assert [entry["renewedRole"] for entry in candidate_renew if "renewedRole" in entry] == ["management", "lab"]
candidate_probe = probe_summary(next(entry for entry in candidate_renew if "connectCode" in entry))
candidate_lease_entries = lines("candidate-onlink-v2/candidate-dhcp-lease.log")
candidate_management = next(entry for entry in candidate_lease_entries if entry.get("role") == "management")
candidate_netplan = [entry for entry in candidate_lease_entries if "netplanFile" in entry]
assert candidate_netplan and all("dhcp4: true" in entry["content"] and "routes:" not in entry["content"] and "gateway4:" not in entry["content"] for entry in candidate_netplan)
assert candidate["exitCode"] == 1
assert all(phrase in candidate_outputs for phrase in (cleanup_phrase, sg_phrase, key_phrase))
report["candidateOnlinkV2"] = {
    "childExitCode": candidate["exitCode"], "elapsedSeconds": candidate["elapsedSeconds"],
    "originalSourceUnchanged": candidate["sourceBefore"] == candidate["sourceAfter"] == strict["sourceAfter"],
    "exactOwnedPortReadback": True, "renewedRoles": ["management", "lab"],
    "probe": candidate_probe,
    "managementServerMatchesQueriedNeutronDHCPPort": candidate_management["serverIsNeutronDHCPPort"],
    "managementLeaseHasRouter": "ROUTER" in candidate_management["lease"],
    "managementLeaseHasRoutesField": "ROUTES" in candidate_management["lease"],
    "netplanFilesObserved": len(candidate_netplan), "netplanDHCPNoStaticRoutesObserved": True,
    "trackedAbsence": 8, "securityGroupGETAbsence": True, "keypairGETAbsence": True,
    "limit": "API readback and lease mtime change do not prove DHCP agent reload completed or establish exact DHCP ACK options. First renew only; no bounded convergence experiment in this run.",
}
candidate3 = result("candidate-onlink-v3/probe-run-result.json")
candidate3_events = lines("candidate-onlink-v3/probe-live.log")
candidate3_outputs = "".join(event.get("Output", "") for event in candidate3_events)
candidate3_ack = lines("candidate-onlink-v3/candidate-ack-capture.log")[0]
candidate3_leases = lines("candidate-onlink-v3/candidate-dhcp-lease-0.log")
candidate3_management = next(entry for entry in candidate3_leases if entry.get("role") == "management")
expected_prefix = ipaddress.IPv4Network((candidate3_management["lease"]["ADDRESS"], candidate3_management["lease"]["NETMASK"]), strict=False)
encoded121 = bytes.fromhex(candidate3_ack["option121Hex"])
decoded121 = []
cursor = 0
while cursor < len(encoded121):
    width = encoded121[cursor]
    cursor += 1
    assert width <= 32
    significant = (width + 7) // 8
    assert cursor + significant + 4 <= len(encoded121)
    destination = ipaddress.IPv4Network((ipaddress.IPv4Address(encoded121[cursor:cursor + significant] + bytes(4 - significant)), width), strict=False)
    cursor += significant
    nexthop = ipaddress.IPv4Address(encoded121[cursor:cursor + 4])
    cursor += 4
    decoded121.append({"destinationMatchesManagementPrefix": destination == expected_prefix,
                       "prefixBits": width, "isDefault": width == 0, "zeroNextHop": nexthop == ipaddress.IPv4Address(0)})
assert len(decoded121) == 1 and decoded121[0]["destinationMatchesManagementPrefix"] and not decoded121[0]["isDefault"] and decoded121[0]["zeroNextHop"]
assert candidate3_ack["serverIP"] == candidate3_management["lease"]["SERVER_ADDRESS"]
assert candidate3_management["serverIsNeutronDHCPPort"]
candidate3_renew = lines("candidate-onlink-v3/candidate-renew-0.log")
candidate3_probes = [probe_summary(entry) for entry in candidate3_renew if "connectCode" in entry]
assert len(candidate3_probes) == 2
assert all(probe["connectCode"] == 0 and probe["sourceRole"] == "lab" and len(probe["defaults"]) == 1 and probe["defaults"][0]["role"] == "lab" for probe in candidate3_probes)
assert candidate3["exitCode"] == 0
assert all(phrase in candidate3_outputs for phrase in (cleanup_phrase, sg_phrase, key_phrase))
report["candidateOnlinkV3"] = {
    "childExitCode": candidate3["exitCode"], "elapsedSeconds": candidate3["elapsedSeconds"],
    "originalSourceUnchanged": candidate3["sourceBefore"] == candidate3["sourceAfter"] == strict["sourceAfter"],
    "ackOwnedMACFilter": candidate3_ack["ownedManagementACK"],
    "ackServerMatchesNeutronLeaseServer": True,
    "ackRouterOptionPresent": bool(candidate3_ack["optionRouter"]),
    "ackClasslessRoutes": decoded121,
    "ack249PayloadBytes": len(bytes.fromhex(candidate3_ack["option249Hex"])),
    "managementLeaseHasRoutesField": "ROUTES" in candidate3_management["lease"],
    "strictProbesAfterConvergence": candidate3_probes,
    "trackedAbsence": 8, "securityGroupGETAbsence": True, "keypairGETAbsence": True,
    "interpretation": "With a single owned port UPDATE, wait and ACK capture, valid nonempty onlink option121 was delivered; sole Lab default passed while option3 was still advertised.",
    "limits": ["Candidate external mutation; production unchanged and final fresh CREATE/ON-OFF/reboot/Reset not validated by this probe-only run.",
               "This does not prove blank router suppression or identify the installed daemon version.",
               "Absence of ROUTES in the lease file is not evidence of option121 absence on wire; this run directly observes option121."],
}
(OUT / "postadmin-observations.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf8")
print(json.dumps({"status": "PASS", "sourceFiles": strict["sourceAfter"]["files"], "firstTCPPasses": len(first), "strictChildExit": strict["exitCode"], "candidateV2ChildExit": candidate["exitCode"], "candidateV3ChildExit": candidate3["exitCode"], "observedAgents": len(agents), "rawInputs": len(provenance)}))
