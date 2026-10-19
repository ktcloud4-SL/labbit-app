"""Read archive members in memory; never extract or execute archive contents."""
from pathlib import Path
from datetime import datetime, timezone
import hashlib
import json
import tarfile

OUT = Path(__file__).resolve().parent
STAGE = OUT.parent / "lbt174-routing-postadmin-20261016/final-verified"
manifest = json.loads((STAGE / "source-manifest.json").read_text(encoding="utf8"))
metadata = json.loads((STAGE / "linux-source-archive.json").read_text())
archive = STAGE / "linux-source.tar.gz"
errors = []
with tarfile.open(archive, "r:gz") as tf:
    members = tf.getmembers()
    names = {member.name: member for member in members}
    # Old tracked Evidence has its own tested-source.json files. Select the
    # explicit live runner root, not a filename suffix heuristic.
    prefix = "source/"
    embedded = json.load(tf.extractfile(names[prefix + "tested-source.json"]))
    assert embedded["files"] == manifest["files"]
    for path, digest in manifest["files"].items():
        member = names.get(prefix + path)
        if member is None:
            errors.append({"path": path, "failure": "missing"})
            continue
        data = tf.extractfile(member).read().replace(b"\r\n", b"\n")
        if hashlib.sha256(data).hexdigest() != digest:
            errors.append({"path": path, "failure": "hash"})
    unsafe = [m.name for m in members if m.name.startswith("/") or ".." in Path(m.name).parts or m.issym() or m.islnk()]
    private = [m.name for m in members if "/.local/" in "/" + m.name or "/.git/" in "/" + m.name or m.name.endswith("clouds.yaml")]
    archive_hash = hashlib.sha256(archive.read_bytes()).hexdigest()
    result = {"recordedUTC": datetime.now(timezone.utc).isoformat(), "archiveSHA256": archive_hash, "metadataArchiveHashMatches": archive_hash == metadata["archiveSHA256"], "sourceCount": len(manifest["files"]), "sourceSHA256": manifest["sourceSHA256"], "embeddedManifestMatches": embedded["files"] == manifest["files"], "sourceErrors": errors, "archiveUnsafePathsOrLinks": unsafe, "privateRuntimePathsIncluded": private, "memberCount": len(members), "noCloudCalls": True}
(OUT / "native-archive-identity.json").write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf8")
print(json.dumps(result, ensure_ascii=False))
assert not errors and not unsafe and not private and result["metadataArchiveHashMatches"]
