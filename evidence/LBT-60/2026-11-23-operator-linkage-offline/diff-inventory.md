# Full base → HEAD + local/untracked inventory

Prior whole-PR audit supports unchanged canonical source only. New local sources and impacted consumers were re-read; historical evidence is not a current live run. Ignored .local files are test binaries, disposable PG data, raw logs and harnesses, not deployment inputs.

| Path | Scope | Treatment |
| --- | --- | --- |
| `.gitleaks.toml` | product / operator and affected consumer | Prior PR change, inspected against owning SSOT |
| `Makefile` | product / operator and affected consumer | Prior full audit reused; unchanged source checked |
| `cmd/labbit-dr-control/main.go` | product / operator and affected consumer | Fresh linkage review + affected regression |
| `cmd/labbit-dr-control/main_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `cmd/labbit-dr-evidence/main.go` | product / operator and affected consumer | Fresh linkage review + affected regression |
| `cmd/labbit-dr-evidence/main_test.go` | test / regression fixture | Fresh linkage review + affected regression |
| `contracts/http/openapi.yaml` | docs / deployment boundary | Prior full audit reused; unchanged source checked |
| `db/migrations/000012_worker_control.sql` | runtime/DB owning contract | Prior full audit reused; unchanged source checked |
| `db/migrations/000013_worker_control_resume.sql` | runtime/DB owning contract | Prior full audit reused; unchanged source checked |
| `db/migrations/000014_worker_control_resume_applied_time.sql` | runtime/DB owning contract | Prior full audit reused; unchanged source checked |
| `db/migrations/000015_worker_control_restrictions.sql` | runtime/DB owning contract | Fresh linkage review + affected regression |
| `db/migrations/README.md` | runtime/DB owning contract | Fresh linkage review + affected regression |
| `docs/backend/dr-maintenance-integration.md` | docs / deployment boundary | Fresh linkage review + affected regression |
| `docs/backend/dr-mutation-admission.md` | docs / deployment boundary | Prior full audit reused; unchanged source checked |
| `docs/backend/dr-stage3-deployment.md` | docs / deployment boundary | Fresh linkage review + affected regression |
| `docs/backend/dr-worker-control.md` | docs / deployment boundary | Prior full audit reused; unchanged source checked |
| `docs/backend/evidence/LBT-60/2026-11-19-admission-local/audit.md` | evidence / historical record | Prior PR change, inspected against owning SSOT |
| `docs/backend/evidence/LBT-60/2026-11-19-admission-local/validation.json` | evidence / historical record | Prior PR change, inspected against owning SSOT |
| `docs/backend/evidence/LBT-60/2026-11-19-worker-control-local/audit.md` | evidence / historical record | Prior PR change, inspected against owning SSOT |
| `docs/backend/evidence/LBT-60/2026-11-19-worker-control-local/validation.json` | evidence / historical record | Prior PR change, inspected against owning SSOT |
| `docs/backend/evidence/LBT-60/2026-11-20-main-refresh/audit.md` | evidence / historical record | Prior PR change, inspected against owning SSOT |
| `docs/backend/evidence/LBT-60/2026-11-20-main-refresh/validation.json` | evidence / historical record | Prior PR change, inspected against owning SSOT |
| `docs/backend/evidence/LBT-60/2026-11-20-maintenance-integration/audit.md` | evidence / historical record | Prior PR change, inspected against owning SSOT |
| `docs/backend/evidence/LBT-60/2026-11-20-maintenance-integration/validation.json` | evidence / historical record | Prior PR change, inspected against owning SSOT |
| `docs/backend/evidence/LBT-60/2026-11-20-pr196-maintenance-regression/audit.md` | evidence / historical record | Prior PR change, inspected against owning SSOT |
| `docs/backend/evidence/LBT-60/2026-11-20-pr196-maintenance-regression/focused-race.jsonl` | evidence / historical record | Prior PR change, inspected against owning SSOT |
| `docs/backend/evidence/LBT-60/2026-11-20-pr196-maintenance-regression/validation.json` | evidence / historical record | Prior PR change, inspected against owning SSOT |
| `docs/backend/evidence/LBT-60/2026-11-20-pr196-validation/audit.md` | evidence / historical record | Prior PR change, inspected against owning SSOT |
| `docs/backend/evidence/LBT-60/2026-11-20-pr196-validation/baseline-fixture-failure.log` | evidence / historical record | Prior PR change, inspected against owning SSOT |
| `docs/backend/evidence/LBT-60/2026-11-20-pr196-validation/postgres-targeted.log` | evidence / historical record | Prior PR change, inspected against owning SSOT |
| `docs/backend/evidence/LBT-60/2026-11-20-pr196-validation/validation.json` | evidence / historical record | Prior PR change, inspected against owning SSOT |
| `docs/backend/evidence/LBT-60/2026-11-20-worker-control-durable/audit.md` | evidence / historical record | Prior PR change, inspected against owning SSOT |
| `docs/backend/evidence/LBT-60/2026-11-20-worker-control-durable/validation.json` | evidence / historical record | Prior PR change, inspected against owning SSOT |
| `docs/backend/evidence/LBT-60/2026-11-20-worker-control-resume/audit.md` | evidence / historical record | Prior PR change, inspected against owning SSOT |
| `docs/backend/evidence/LBT-60/2026-11-20-worker-control-resume/validation.json` | evidence / historical record | Prior PR change, inspected against owning SSOT |
| `evidence/.gitattributes` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/AUDIT.md` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/artifact-manifest.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/downloads-manifest-attempt01.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/downloads-manifest.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/environment.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/go-credential.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/go-installer-final.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/go-installer-initial.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/go-openstack.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/go-workspacefile.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/harness-manifest.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/harness/build_image.py` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/harness/build_image_attempt01.py` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/harness/finalize_evidence.py` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/harness/init-attempt01.sh` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/harness/init.sh` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/harness/prepare_linux.py` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/payload-manifest-attempt01.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/payload-manifest.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/python-normal.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/python-optimized.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/qemu-attempt01.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/qemu-attempt02.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/source-comparison.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-linux-offline/test-summary.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-regression-diagnosis/AUDIT.md` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-regression-diagnosis/artifact-manifest.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-regression-diagnosis/base-installer-trace.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-regression-diagnosis/base-installer.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-regression-diagnosis/base-openstack-timeout.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-regression-diagnosis/base-openstack.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-regression-diagnosis/base-pty.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-regression-diagnosis/connector-final.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-regression-diagnosis/connector-race-final.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-regression-diagnosis/connector-recheck.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-regression-diagnosis/go-all-final.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-regression-diagnosis/go-build.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-regression-diagnosis/go-vet.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-regression-diagnosis/gofmt-canonical.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-regression-diagnosis/linux-compile.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-regression-diagnosis/prior-source-comparison.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-regression-diagnosis/source-manifest.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-regression-diagnosis/test-summary.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/AUDIT.md` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/README.md` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/artifact-manifest.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/cleanup.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/control-final.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/control-race.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/go-build.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/go-test.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/go-vet.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/gofmt-canonical.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/gofmt-check.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/integration-all.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/integration-vet.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/maintenance-postgres-final.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/maintenance-postgres.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/npm-ci.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/postgres.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/runtime-metadata.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/source-manifest.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/test-summary.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/web-build.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/web-lint.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/web-test-recheck.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/web-test.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-21-stage3-offline/web-typecheck.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/artifact-manifest.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/audit.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/aws-observation.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/cli-preview-results.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/commands.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/disabled-execute.stderr.txt` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/ecr-candidate-tag.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/independent-review.md` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/migration-job-summary.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/operator-offline-tests.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/operator-offline-tests.stderr.txt` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/osaka-observation.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/preview-only-input.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/preview-only-policy.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/preview.stderr.txt` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/readiness.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/readonly-aws.go.txt` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/readonly-metadata-smoke-policy.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/readonly-metadata-smoke-receipt.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/readonly-metadata-smoke-result.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/readonly-metadata-smoke.stderr.txt` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/seoul-control-settings.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/seoul-maintenance-declaration.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/seoul-observation.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/seoul-rds.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/ssot-source.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-live-readiness/verification.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/AUDIT.md` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/artifact-manifest.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/ci-initial.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/commands.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/directory-command.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/final-history-command.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/gitleaks-baseline.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/gitleaks-baseline.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/gitleaks-ci-initial.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/gitleaks-directory-final.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/gitleaks-directory-final.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/gitleaks-final.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/gitleaks-final.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/gitleaks-history-final.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/gitleaks-history-final.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/gitleaks-line-anchor-initial.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/gitleaks-line-anchor-initial.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/gitleaks-line-anchor-second.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/gitleaks-line-anchor-second.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/gitleaks-provenance.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/negative-control-command.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/negative-control.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/negative-control.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/review.md` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/scope-negative-command.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/scope-negative-final.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/scope-negative-final.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-ci-followup/source.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/AUDIT.md` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/artifact-manifest.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/assemble.py` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/cleanup.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/commands.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/contract-metadata.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/diff-inventory.md` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/format.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/go-all.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/go-build.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/go-control-race.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/go-vet.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/index-verification.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/index-verify.py` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/integration-all-final.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/integration-all.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/integration-vet-final.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/integration-vet.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/pg-databases-final.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/pg-databases.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/pg-init-final.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/pg-init.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/pg-start-final.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/pg-start.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/pg-stop-final.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/pg-stop.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/publication-source.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/review-operator-source.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/review-operator.jsonl` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/review-operator.md` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/review-regression.md` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/review-worker.md` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/secret-pattern-scan.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/source-manifest.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/test-summary.json` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/verify-initial.py` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/verify.py` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/web-build.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/web-lint.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/web-test.log` | evidence / historical record | Original bytes / historical evidence only |
| `evidence/LBT-60/2026-11-23-pr196-publication/web-typecheck.log` | evidence / historical record | Original bytes / historical evidence only |
| `internal/connector/installer/installer_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/connector/provider/openstack/active_readiness_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/connector/provider/openstack/ktcloud_d1_cidr_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/connector/provider/openstack/ktcloud_management_route_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/connector/provider/openstack/readiness_diagnostic_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/connector/provider/openstack/readiness_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/drcontrol/awscli.go` | product / operator and affected consumer | Prior full audit reused; unchanged source checked |
| `internal/drcontrol/awscli_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/drcontrol/collector.go` | product / operator and affected consumer | Fresh linkage review + affected regression |
| `internal/drcontrol/collector_adapters.go` | product / operator and affected consumer | Fresh linkage review + affected regression |
| `internal/drcontrol/collector_adapters_test.go` | test / regression fixture | Fresh linkage review + affected regression |
| `internal/drcontrol/contract.go` | product / operator and affected consumer | Fresh linkage review + affected regression |
| `internal/drcontrol/database.go` | product / operator and affected consumer | Prior full audit reused; unchanged source checked |
| `internal/drcontrol/database_integration_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/drcontrol/linkage_test.go` | test / regression fixture | Fresh linkage review + affected regression |
| `internal/drcontrol/protected.go` | product / operator and affected consumer | Fresh linkage review + affected regression |
| `internal/drcontrol/protected_unix.go` | product / operator and affected consumer | Fresh linkage review + affected regression |
| `internal/drcontrol/protected_unix_test.go` | test / regression fixture | Fresh linkage review + affected regression |
| `internal/drcontrol/protected_windows.go` | product / operator and affected consumer | Fresh linkage review + affected regression |
| `internal/drcontrol/protected_windows_test.go` | test / regression fixture | Fresh linkage review + affected regression |
| `internal/drcontrol/restrict.go` | product / operator and affected consumer | Fresh linkage review + affected regression |
| `internal/drcontrol/restriction_integration_test.go` | test / regression fixture | Fresh linkage review + affected regression |
| `internal/drcontrol/sign.go` | product / operator and affected consumer | Fresh linkage review + affected regression |
| `internal/drcontrol/verified.go` | product / operator and affected consumer | Prior full audit reused; unchanged source checked |
| `internal/drcontrol/verified_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/postgres/migrate_integration_test.go` | test / regression fixture | Fresh linkage review + affected regression |
| `internal/postgres/migration_test.go` | test / regression fixture | Fresh linkage review + affected regression |
| `internal/postgres/worker_control.go` | product / operator and affected consumer | Fresh linkage review + affected regression |
| `internal/postgres/worker_control_integration_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/postgres/worker_control_lifecycle_integration_test.go` | test / regression fixture | Fresh linkage review + affected regression |
| `internal/postgres/worker_control_restrict.go` | product / operator and affected consumer | Fresh linkage review + affected regression |
| `internal/postgres/worker_control_resume.go` | product / operator and affected consumer | Fresh linkage review + affected regression |
| `internal/postgres/worker_control_resume_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/postgres/worker_control_test.go` | test / regression fixture | Fresh linkage review + affected regression |
| `internal/server/app/admission_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/server/app/app.go` | product / operator and affected consumer | Fresh linkage review + affected regression |
| `internal/server/app/control_stack.go` | product / operator and affected consumer | Fresh linkage review + affected regression |
| `internal/server/app/dr_maintenance_integration_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/server/app/dr_readback.go` | product / operator and affected consumer | Fresh linkage review + affected regression |
| `internal/server/app/dr_readback_test.go` | test / regression fixture | Fresh linkage review + affected regression |
| `internal/server/app/worker_control_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/server/app/worker_metrics_integration_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/server/connector/registry.go` | product / operator and affected consumer | Prior full audit reused; unchanged source checked |
| `internal/server/connector/registry_close_notification_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/server/connector/router.go` | product / operator and affected consumer | Prior full audit reused; unchanged source checked |
| `internal/server/connector/worker_control_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/server/connectorwss/close_cleanup_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/server/connectorwss/conn.go` | product / operator and affected consumer | Prior full audit reused; unchanged source checked |
| `internal/server/connectorwss/handler.go` | product / operator and affected consumer | Prior full audit reused; unchanged source checked |
| `internal/server/httpapi/admission_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/server/httpapi/dr_maintenance_integration_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/server/labexecution/admission_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/server/maintenance/controlled_worker_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/server/maintenance/gate.go` | product / operator and affected consumer | Fresh linkage review + affected regression |
| `internal/server/maintenance/gate_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/server/provision/controlled_maintenance_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/server/provision/executor.go` | product / operator and affected consumer | Prior full audit reused; unchanged source checked |
| `internal/server/provision/maintenance_resume_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/server/provision/reconciliation.go` | product / operator and affected consumer | Prior full audit reused; unchanged source checked |
| `internal/server/provision/worker_control_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/server/provision/worker_resume_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/server/workercontrol/control.go` | product / operator and affected consumer | Prior full audit reused; unchanged source checked |
| `internal/server/workercontrol/control_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/server/workercontrol/durable.go` | product / operator and affected consumer | Fresh linkage review + affected regression |
| `internal/server/workercontrol/durable_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/server/workercontrol/maintenance_resume.go` | product / operator and affected consumer | Prior full audit reused; unchanged source checked |
| `internal/server/workercontrol/maintenance_resume_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/server/workercontrol/restrict.go` | product / operator and affected consumer | Fresh linkage review + affected regression |
| `internal/server/workercontrol/resume.go` | product / operator and affected consumer | Prior full audit reused; unchanged source checked |
| `internal/server/workercontrol/resume_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/server/workercontrol/settings.go` | product / operator and affected consumer | Prior full audit reused; unchanged source checked |
| `internal/server/workercontrol/settings_admission_test.go` | test / regression fixture | Prior full audit reused; unchanged source checked |
| `internal/testsupport/workercontrolfake/resume.go` | product / operator and affected consumer | Prior full audit reused; unchanged source checked |
| `internal/testsupport/workercontrolfake/store.go` | product / operator and affected consumer | Prior full audit reused; unchanged source checked |
| `runtime/README.md` | runtime/DB owning contract | Fresh linkage review + affected regression |
| `runtime/contract.yaml` | runtime/DB owning contract | Fresh linkage review + affected regression |
| `runtime/dr-control.md` | runtime/DB owning contract | Fresh linkage review + affected regression |
