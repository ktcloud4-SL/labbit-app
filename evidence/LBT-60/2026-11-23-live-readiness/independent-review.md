# PR196 live-readiness independent review — 2026-11-23

Reviewer: /root/audit_pr196_operator. Read-only review only; no AWS/DB/API/Service/Provider call, source change, push, deploy or merge performed. Parent asked to assess the minimal safe preparation order and separate partial trial prerequisites from full DR prerequisites. PRE_MERGE_AUDIT.md was read in the preceding independent operator audit.

## Scope and fixed versions

App PR196 HEAD94fe1f61d7e398761291a967fab9820bfb08bc3d; main753eecbff737409a4c9cc8c542f3c6baa6acf0da. Reviewed deployment checklist, common trial plan, Runtime operator contract and CLI code at this local clean HEAD, and parent's supplied raw JSON observations. No complete new source-code audit was requested in this pass; prior operator audit applies to source already reviewed, while this pass assesses deployment readiness and scope boundaries.

Raw files under C:/Users/user/Documents/ChatGPT/3차 2/tmp/lbt60-live-readiness-20261123/:

- aws-observation.json SHA256 BE6510C1816181B0C70ACC961F72DF4E0CEDBCAFC0E7FF7BC3E9B51FBB7E3821
- seoul-observation.json SHA256 1F2F85777CEB5F7E1A3D0EC7BCB10982547608A0BED1A52698206810E7CBB435
- seoul-control-settings.json SHA256 0EDC5B16FBFBE45D5368A92690A3652DD7B87DB143FF29B8DA4E8D65AF9DA910
- osaka-observation.json SHA256 224D604C4EB3BC6019166E2441FC74F58BE2EB57ABB348F99CAB7CA4289DA9E2

Kubeconfig files were intentionally not read or copied. Observation window approximately2026-11-23T01:12Z–01:14Z. These historical observations are not a continuously fresh barrier.

## Confirmed observations and unverified boundaries

- Seoul backend2/2 Ready, exact old sha753eecb/digestbab1869ef211ddc0f1a559e4c9d543d5add1d59269e5db85aad2e384bed42419. Not PR196 candidate deployment.
- Argo backend/root automatic sync enabled and GitOps observed revisionab830d7c9e046b0621477192aca39c6b269cbf30. Therefore live patching can self-heal or trigger unintended rollout/migration unless an explicit bounded change/restoration plan is used.
- Worker roles includeapi,realtime,worker,preview. New workercontrol/admission opt-in env absent. This is existing main behavior, not evidence of #196 malfunction.
- Migration Job from sha753eecb completed, but this does not establish actual schema versions/checksums, twelve-to-fourteen migration application, backup, restore or rollback readiness.
- Osaka app Deployments/Pods empty; Argo read exit1. Parent reports missing ApplicationCRD/Argo deployment0 and EKSactive; those specific facts are parent-supplied beyond these four JSONs. This does not block a Seoul-only #196 partial trial.
- Osaka replica available/replicating at01:14Z; recent restart/backup and stopped/resumed events are preserved. Lag-1 is unavailable information, not zero. Existing Lag policyFAIL remains. No promotion/DNS permission follows from available status.
- Candidate sha94fe1f6 ECRtag absent is parent-supplied. Candidate source/CI success does not mean a deployable pinned image already exists.

## Findings before any correction

No new confirmed product defect from these observations. Documentation/evidence issues:

| ID / severity | Location at HEAD94fe1f6 | Reproduction / impact | Minimal correction / check |
| --- | --- | --- | --- |
| RDY-DOC-01/P3 | docs/backend/dr-stage3-deployment.md:3 and:29 | Still identifies c11b70a/local-only and currently-down AWS. Current observation is HEAD94fe1f6 and AWS running. Wrong tested/deployed source inference possible. | Preserve historical date but add current dated actual status and exact observation references; do not claim deployment. No unit test needed. |
| RDY-DOC-02/P3 | docs/backend/dr-maintenance-integration.md:78 | Says trusted Pause/Resume/Authorizer/Rechecker all unimplemented although new opt-in Resume CLI/verifier candidate exists. Can cause duplicate implementation. | Distinguish implemented-but-not-operationally-accepted Resume adapter from still-missing Pause/init path, real safety collectors and key operation. No new controller or endpoint is demanded solely for wording. |
| RDY-EVID-03/Unverified data, not product finding | seoul-control-settings.json:30 | maintenance_mode=null while data_keys containsmode. This file alone cannot prove actualopen. Parent saysopen but needs a separate exact data.mode/mounted-file evidence. | Link existing rawmode evidence if available; otherwise obtain a safe read-only value and each serving Pod mounted mode observation before asserting a live barrier. Avoid full ConfigMap/Secret dumps. |

## Minimal next order — no changes yet

1. Freeze the intended **test scope**, source94fe1f6, trial/RUN, target namespace/DB, participants and non-use work window in the plan. This is planning, not a request to rebuild all DR prerequisites.
2. Complete read-only readiness gaps using existing authenticated access only: candidate artifact metadata/digest; exact Pod/role/binding and mounted mode; current migration versions+checksums and relevant table/constraint existence; DB target/writer identity; current operation/lease/UNKNOWN inventory; existing backup/PITR/restore evidence and named recovery owner. Query only required metadata, never print DSN/Secrets or full payloads. A completed old migration Job is not a substitute for SQL inspection. A fresh disposable local integration rerun is not required when code is unchanged and exact-source evidence already exists.
3. Run **preview only** using protected EnableExecution=false, synthetic non-secret valid policy/input clearly marked PREVIEW. Preview validates input and returnsHOLD; it proves no STS/IAM, schema, signed safety or actual ability to pause/resume. Do not fabricate LIVE_READBACK/PASS artifacts or call--execute to obtain readiness.
4. Prepare a bounded actual-change proposal before applying it: candidate image build/publish to a fixed digest withoutmainmerge; whether candidate gets an isolated test deployment or replaces shared Backend; exact migration12–14 forward-only application/backup/recovery; Argo freeze target and restoration; durable initial PAUSE_NEW/binding; authenticated operator Pause/init method reusing owning Store methods; role and secret access; trial-only workloads/Provider budget and cleanup. Current CLI intentionally does notinit/Pause/applymode; ad-hoc production SQL or synthetic proof cannot substitute.
5. After allowed change window, candidate schema/config/image are in place, begin closed/negative control and API regression evidence. Actual multi-Pod tests require coverage of every serving candidate Pod with fixed revision/epoch and explicit restart/HPA freeze. #194 remainingdrain/replay and #196 additionalPause may share a plan but use distinct expectations/RUN phases.
6. Execute authenticated **resume** only when the current owning six safety conditions are genuinely measured and freshly signed, authorizer/observer keys and principal are controlled, authoritative control Writer and unresolved work are known, and a reviewed transition/rollback procedure is available. If unavailable, leave resumeNOT_VERIFIED while allowing other safe trial rows to be completed. Do not weaken the verifier or label a synthetic signatureLIVE merely to finish the demo.
7. Finish per-row evidence, approved restoration/cleanup and independent QA. RetainFAIL/NV/HOLD and distinguish candidate partial acceptance from full DR.

## Do not unnecessarily block the partial trial on full DR

**Seoul-only PR196 partial trial must not require first**: Osaka app/Argo activation, Worker-inclusive Osaka profile deployment, Osaka RDS promotion, Route53switch, entire22-step failover/failback, officialRTO/RPO attainment, or repairing the idle time-based ReplicaLag15sec issue. Those are later/fullDR paths. Need not introduce a new web approval system/controller/abstract framework: the narrow trusted operator method that preserves existing authorization, exact binding and journaling is sufficient as an implementation direction, subject to its real validation.

**But do not loosen a specific operation's existing contract**: a real #196 Resume cannot use fabricatedSingleWriter/Fencing/POD/InventoryPASS just because no Region switch is occurring. Collect the scoped real safety conditions required by the current verifier; if impossible, report that row unverified and continue non-resume parts. Current globalDRHOLD is an overall acceptance result; it does not prohibit allowed read-only preparation or bounded non-DR feature trials, nor is it permission to promote or route traffic.

## Final readiness judgment

Read-only preparation and no-I/Opreview are possible now. PR196 deployment/migration, shared closed/open testing and authenticated resume are **not yet ready on submitted observations**, due to concrete missing candidate image/schema/recovery and operator/control/safety input readiness, not a newly established code defect. Parent's plan to update existing docs, complete allowed read-only inspections and show a service-impacting change proposal before execution is proportionate. Avoid claiming2/2oldPods or replicaavailable as #196 livePASS. No live test executed by this reviewer.

## Incremental review — live-prep worktree / pre-smoke harness safety

Reviewed independently on2026-11-23 after the parent addressed documentation findings. Worktree C:/Users/user/Documents/ChatGPT/3차 2/tmp/labbit-dr-stage3-live-prep-20261123, HEAD94fe1f61d7e398761291a967fab9820bfb08bc3d plus exactly two modified docs and new evidence/LBT-60/2026-11-23-live-readiness/. Product source changes0. Parent's synthetic preview and54-test raw log were read and parsed; this reviewer did not execute AWS/DB/service/code/push operations.

### Previous observations resolved or scoped

- RDY-DOC-01: resolved by dated11/23 section, fixed candidateHEAD/main, historical11/21-down condition retained as history, explicit old-image/new-candidate distinction.
- RDY-DOC-02: resolved by separating implemented opt-in Resume verifier candidate from unaccepted real operator setup and missingPause/init/livecollector/key operation.
- RDY-EVID-03: ConfigMap declaredmode now independently evidenced by seoul-maintenance-declaration.json (01:17:27Z, same UID/resourceVersion, modeopen). Per-Pod mounted file remains explicitly not read. No live admission barrier falsely asserted.
- migration-job-summary logsapplied_count11/latest_known_version11 are explicitly selected fields, not current directSQLchecksum proof. ECRmissing exacttag is explicitly selected transcript outcome with approximate timestamp, not claimed complete raw response or allimage absence.
- Readiness top-level product_changedfalse/live_test_readyfalse and overallHOLD/false flags match changes and evidence. Prior image, runtime, unsigned preview and realobservations not conflated.

### Raw evidence read / checked

Read readiness.json, both docs, all JSON artifacts includingssot-source.json, offlinepreviewfixture, CLIpreviewresults, AWS/Seoul/Osaka observations, migration summary, maintenance declaration, ECRexacttag lookup and SeoulRDSmetadata. Parsed operator-offline-tests.jsonl: **54 TestPASS / 0FAIL / 0SKIP**,53drcontrol+1CLI; these are unit/non-race/nointegration as correctly described. Previewexit0/PREVIEW_ONLY/NO_EXTERNAL_IO and disabledexecuteexit2/EXECUTION_DISABLED withHOLD and bothfalse flags match CLI source. Stderr includes only genericHOLD/noautoretry. Fixture has fakeaccount, fakepublickeys, EnableExecutionfalse, nonexistentAWSexe/DB, so it is not productionPolicy or authorization. Independent-review.md is the earlier historical report; this appendix records resolution rather than rewriting prior findings.

### Planned `.local/readonly-aws/main.go` safety review

Source review only; no execution by reviewer.

- Policy parsing uses existingstrict decoder, Validate and rejectsEnableExecutiontrue.
- WrapperRunner permits only exact STSget-caller-identity and RDSdescribe-db-instances command pairs. ExistingNewAWSCLI constructs official endpoints/profile/region/timeouts and bounded arguments; helper does not accept extra command arguments.
- Only Identity, VerifyDatabase and second VerifyDatabase with intentionally wrongResourceID are called. No pgx/SQLpool, S3read, drcontrol.Execute, ResumeStore, AuthorizeResume, key signing, mutation, Provider or service path exists.
- Expected successful external request count3: STS1 + RDS2. Context10seconds plus per-call boundedAWSCLI. WrongID changes only localPolicycomparison; same target is read again, no change request.
- Realaccount/exactARN match and realRDS ARN/ResourceID/status/engine/endpoint/port match may be claimed if actual result succeeds. ActorPrincipal/actions authorization, TLS/SQLWriter, artifactbinding/signatures or SingleWriterPASS may **not** be claimed; syntheticTrustKeys/DBname/user exist only to satisfyPolicyshape and are not operational setup.
- ExistingCommandRunner uses a protected explicit executable/Profile assumed controlled by operator. This wrapper is a one-off smoke harness, not a new product approval/operator entrypoint.

**No confirmed blocking defect in the proposed readonly smoke boundary.** Reproducibility requires preserving helper exactsource/hash, nonsecretPolicy with synthetic/real field separation, invocation/UTC/exit/result and sourceHEAD before publication. A `.local`-only helper must not become an undocumented dependency; archive as evidence/reproduction material rather than advertise it as an approved operating command. Logs should retain failures and describe up to3readonlycalls, not only finalsuccess. These are recording requirements, not a demand for new product abstractions or liveDR.

### Decision

The newdocs/evidence accurately retain preparation-only scope. The parent's proposed standalone STS/RDS readonlytransport/targetmismatch smoke can remain inside existingreadonlyauthorization and does not affect Seoul workload. No product change or cloudmutation is necessary for this smoke. All actualdeploy/schema/Pause/Resume/drill gates in the previous section remain unresolved until separately prepared and tested. This review is not approve/merge/deploy/DRGO.

## Final smoke/evidence increment — 2026-11-23

Reviewed parent-produced real smoke result/receipt/policy, archived helper source, audit.json, verification.json, commands.json and latest two docs/readiness packet. Reviewer performed only local file reading and Git-normalized hash calculations; no AWS, DB, service, source edit or push.

- Raw result: execution_gateHOLD, recovery_authorizedfalse/cloud_verifiedfalse, external_calls3, identity_metadataMATCH/rds_metadataMATCH/wrong_resource_idREJECTED, reasonREADONLY_METADATA_SMOKE_PASSED. Actual smoke interval01:24:05.9173048Z–01:24:08.2868249Z is inside receipt interval01:24:05.7337860Z–01:24:08.2926185Z, exit0.
- Receipt exactsource94fe1f61d7e398761291a967fab9820bfb08bc3d. Archived readonly-aws.go.txt SHA2563fcd18335a9ccf0050eaeda45f0df920f9fe6a207f1a509c7d8775c04ee5038c independently matches original reviewed helper; current local helperbinary SHA256b31cb510b90e42095df7204f95d962132848cb7b8ad366c05e316f447bf63d56 independently matches receipt. Binary is a local reproduction dependency, not a production signed build.
- Policy uses realaccount/exactIAMARN and exactSeoulRDS ARN/ResourceID/endpoint/port, with EnableExecutionfalse. Targetlabels, keys, Principal/action and DBname/user are synthetic placeholders. This smoke compares identitymetadata and database metadata only; it does not call Authenticate, approve anything, sign observations, querySQL/S3 or change a control state.
- Independently recalculated all72 sourcefile Git-normalized blobs from verification.json: **recorded mismatch0**. Against original inventory, **70 unchanged** and only docs/backend/dr-maintenance-integration.md and docs/backend/dr-stage3-deployment.md changed. No product, runtime, schema, dependency, workflow or image-delivery change in this increment.
- Both docs and readiness/audit correctly label actual adapter smoke as readonlymetadata and explicitly deny inference of productionPolicy authorization, signedproof, SQL/TLSWriter, Pause/Resume, SingleWriter or DRGO. Audit reuses prior wholePR source/test audits for unchanged code and reports this turn's54unit tests separately; appropriate for doc/evidence-only delta. Reviewer's54event recount was independent, but actualAWSresults were read rather than independently re-executed.
- Commands inventory is explicitly not a chronological raw transcript; log projection, approximateECRtime, nofullrawlogs and mountedPodmode not observed remain stated. Sourcecopy/reproduction instructions preserve the former`.local`helper dependency without treating it as an operating entrypoint. No secret/privatekey/DSN credential material found in reviewed packet; real resource identifiers are selected metadata, not credentials.

**Final scoped judgment: no new confirmed blocking finding in this read-only readiness/documentation/evidence increment.** Parent may finalize a non-authoritative SHA manifest after copying this review; manifest does not sign or approve runtimeactions. Keep PRDraft/merge/deploy boundaries and existingReplicaLagFAIL/fourNOT_VERIFIED/DRHOLD. Livefunctional test is stillNOT_STARTED_PRECONDITIONS_MISSING. This review does not require freshfullGo/Web/PG/Linux runs where no source has changed; existing exactsource audit/testEvidence is reusable with its limits.
