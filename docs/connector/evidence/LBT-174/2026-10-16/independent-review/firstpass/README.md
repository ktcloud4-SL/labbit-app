# LBT-174 independent first-pass review

Reviewed base/HEAD `4691c0ae8a43955549ae2b9e8bc4f5a464c30001` with uncommitted first-pass source, pinned by snapshot-manifest.json. The production checkout was not changed. No cloud/remote mutation occurred. Snapshot includes tracked and untracked files; extra zz_independent tests are external reviewer fixtures and are listed in probe-manifest.json.

## Confirmed findings reported before fixes

| ID | Severity | First-pass location | Reproduction and impact | Regression |
|---|---|---|---|---|
| F174-R006 | High | internal/connector/wss/handler.go:1022-1036 | onControlDisconnected checks old epoch, unlocks, SetSender installs new owner, then disconnect code rereads and cancels new previewOwner and closes the shared current registry. Normal schedule 10/10 PASS; 20 ms delay only after Unlock gives 3/3 FAIL. | Retire only the old owner/registry snapshot atomically with epoch check; delayed I/O/callbacks must preserve a new owner/session. OPEN admission and CLOSE retirement must share the authority gate. |
| F174-R007 | Medium | internal/connector/preview/client.go:154-179 | Gateway receives HTTP Upgrade but withholds response, then context is canceled. Actual socket remains blocked beyond 250 ms, 3/3 FAIL. v1.5.3 gorilla DialContext sets deadline but does not close the established raw socket on cancellation during HTTP response read. The tunnel learns this socket only after successful Upgrade, so its Close cannot interrupt it. Default handshake bound is 10 seconds; pending socket and worker slot linger until that bound or peer response. | Track/close the raw socket during Upgrade, including cancel-before-registration and successful ownership handoff. Cancel/Close/retirement must promptly unblock the pending handshake without touching a newer attempt. |

Parent was notified immediately of both findings before source fixes. New source is being reviewed separately; this historical snapshot and failure logs remain unchanged.

## Checks run

Preview/WSS/App package normal tests and race tests each passed on this pinned source (boundary-tests.log and boundary-race.log). Race toolchain: Go 1.27.1, CGO_ENABLED=1, local WinLibs GCC. These are mock/in-process loopback boundaries and exclude real OpenStack. The new local integration test was read but not executed by this reviewer. The author owns authorized real-cloud execution and must preserve its result/source identity separately.

## Scope and requirement checks

Read manager/session/client, Handler Preview owner/admission/dispatcher/disconnect/supervisor consumer, App lifecycle provider wrapper and shutdown, lifecycle/owner/handler regression tests, and new external-package local OpenStack fixture with test-only Provider exports and inherited resource teardown helpers. No contract/runtime/dependency/migration change was introduced by this snapshot. Direct Jira LBT-174 and Confluence D18 v5/decision completion v11/test strategy v3 were read during the initial review (../lbt174-initial-review-4691c0a/ssot-read.json). Owning contracts/connector/README.md section7b, preview-control/data JSON Schemas, runtime Preview settings/lifetime, TESTING.md and repository AGENTS/CONTRIBUTING were checked.

The fixture composes real App BuildConnector, actual Control WSS listener and Provider Provision/Reset/Cleanup; Mock SaaS/Gateway provide authority. HTTP byte stream crosses arbitrary Binary boundaries and exact VM response is parsed. It checks sequential tunnels, two logical sessions, CLOSE isolation, shared PTY/SFTP after Preview close, safe application/port/target errors, old-generation retirement and fresh HTTP after Reset, logical cleanup and forbidden contents on Control. Stale ATTACHED/owner/backpressure fault cases are unit/loopback fixtures, not a real SaaS Browser/C3 assertion. Management SG/keypair helpers use Delete acknowledgement; tracked Lab resources have exact GET absence checks.

## Limits

This is an intermediate review, not final PR approval. Real local run and final source identity were pending. No KT Cloud/C3/browser E2E was executed. Final full-diff/traceability/source-Evidence review is still required after the reported fixes and real-run result. No push/PR/merge authority is inferred.
