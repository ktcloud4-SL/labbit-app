"""Read-only upstream code analysis. No OpenStack or local service calls."""
import ast
import hashlib
import json
import re
from datetime import datetime, timezone
from pathlib import Path
from types import SimpleNamespace
from urllib.request import Request, urlopen

OUT = Path(__file__).resolve().parent

def fetch(url):
    req = Request(url, headers={"User-Agent": "LBT174-independent-readonly-audit"})
    with urlopen(req, timeout=30) as response:
        return response.read()

commit = json.loads(fetch("https://api.github.com/repos/openstack/neutron/commits/master"))["sha"]
paths = {
    "ovn_client": "neutron/plugins/ml2/drivers/ovn/mech_driver/ovsdb/ovn_client.py",
    "ovn_utils": "neutron/common/ovn/utils.py",
    "ovn_constants": "neutron/common/ovn/constants.py",
    "dnsmasq": "neutron/agent/linux/dhcp.py",
}
sources = {}
provenance = []
for label, path in paths.items():
    url = f"https://raw.githubusercontent.com/openstack/neutron/{commit}/{path}"
    raw = fetch(url)
    artifact = label + ".py.txt"
    (OUT / artifact).write_bytes(raw)
    sources[label] = raw.decode("utf8")
    provenance.append({"path": path, "url": url, "artifact": artifact,
                       "sha256": hashlib.sha256(raw).hexdigest(), "bytes": len(raw)})

def isolated_function(label, name, globals_):
    tree = ast.parse(sources[label])
    node = next(n for n in ast.walk(tree) if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and n.name == name)
    node.decorator_list = []
    module = ast.Module(body=[node], type_ignores=[])
    exec(compile(module, provenance[list(paths).index(label)]["url"], "exec"), globals_)
    return globals_[name], {"start": node.lineno, "end": node.end_lineno}

ovn_const = SimpleNamespace(
    DHCP_DISABLED_OPT="dhcp_disabled",
    SUPPORTED_DHCP_OPTS_MAPPING={4: {"router": "router", "classless-static-route": "classless_static_route"}},
    OVN_STR_TYPE_DHCP_OPTS=[], OVN_MAP_TYPE_DHCP_OPTS=["classless_static_route"],
)
get_globals = {
    "constants": ovn_const,
    "portbindings": SimpleNamespace(VNIC_TYPE="binding:vnic_type", VNIC_NORMAL="normal", VNIC_BAREMETAL="baremetal"),
    "is_network_device_port": lambda p: False,
    "edo_ext": SimpleNamespace(EXTRADHCPOPTS="extra_dhcp_opts"),
    "_is_dhcp_disabled": lambda opt: False,
}
isolated_function("ovn_utils", "is_dhcp_option_quoted", get_globals)
isolated_function("ovn_utils", "is_dhcp_option_a_map", get_globals)
get_opts, get_lines = isolated_function("ovn_utils", "get_lsp_dhcp_opts", get_globals)
port = {"id": "synthetic-port", "extra_dhcp_opts": [
    {"opt_name": "router", "opt_value": "", "ip_version": 4},
    {"opt_name": "classless-static-route", "opt_value": "", "ip_version": 4},
]}
disabled, converted = get_opts(port, 4)
assert not disabled and converted == {"router": "", "classless_static_route": "{}"}
merge, merge_lines = isolated_function("ovn_client", "_merge_map_dhcp_option", {})
subnet = {"classless_static_route": "{169.254.169.254/32,192.0.2.2, 0.0.0.0/0,192.0.2.1}"}
merged_empty = merge(None, "classless_static_route", converted, subnet)
merged_onlink = merge(None, "classless_static_route", {"classless_static_route": "{192.0.2.0/24,0.0.0.0}"}, subnet)
assert "0.0.0.0/0,192.0.2.1" in merged_empty
assert "0.0.0.0/0,192.0.2.1" in merged_onlink
format_opt, format_lines = isolated_function("dnsmasq", "_format_option", {
    "re": re, "constants": SimpleNamespace(IP_VERSION_6=6),
})
lines = [format_opt(None, 4, "port-synthetic", name, "")
         for name in ("router", "classless-static-route")]
assert lines == ["tag:port-synthetic,option:router,", "tag:port-synthetic,option:classless-static-route,"]
onlink = format_opt(None, 4, "port-synthetic", "classless-static-route", "192.0.2.0/24,0.0.0.0")
report = {
    "observedAtUTC": datetime.now(timezone.utc).isoformat(),
    "mode": "upstream pinned source AST extraction; dependency stubs; no Neutron/dnsmasq daemon execution",
    "neutronCommit": commit, "provenance": provenance,
    "functions": {"get_lsp_dhcp_opts": get_lines, "_merge_map_dhcp_option": merge_lines, "_format_option": format_lines},
    "ovn": {"convertedEmpty": converted, "mergedEmpty": merged_empty, "mergedOnlink": merged_onlink,
            "observation": "This upstream OVN path retains the subnet's default map entry with empty or onlink-only port map; deployed backend/version not established by this test."},
    "dnsmasq": {"emptyPortLines": lines, "onlinkPortLine": onlink,
                "observation": "Upstream DHCP agent emits host-tagged explicit empty options. This does not establish installed agent output/tag matching/ACK contents or guest application."},
    "syntheticAddresses": "RFC5737 documentation-only; no request issued to them",
    "status": "PASS",
}
(OUT / "upstream-dhcp-observations.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf8")
print(json.dumps({"status": "PASS", "commit": commit, "sourceFiles": len(provenance), "ovnDefaultRetainedCases": 2, "dnsmasqLinesChecked": len(lines) + 1}))
