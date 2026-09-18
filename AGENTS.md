# Labbit Agent Instructions

This file contains repository-wide instructions for Codex and other AI coding agents.

## Working conventions

- Follow `CONTRIBUTING.md` for branch, commit, PR, and merge conventions.
- Keep changes scoped to the Jira item or the explicit task.
- Do not duplicate product or architecture decisions in code comments or ad-hoc docs when an existing SSOT already owns them.
- Read the applicable contract before changing a producer or consumer.

## Repository SSOT

- HTTP API: `contracts/http/openapi.yaml`
- SaaS ↔ Connector protocol: `contracts/connector/`
- Browser Terminal/Live protocol: `contracts/realtime/`
- PostgreSQL physical schema: `db/migrations/`
- Application runtime contract: `runtime/`

Implementation must not silently invent fields, states, protocol behavior, or runtime semantics that contradict the applicable SSOT. If the contract itself must change, update the contract explicitly and consider all affected producers and consumers.

## Validation

Use the narrowest relevant checks while iterating, then run the repository-level validation before marking substantial work complete.

```bash
make setup
make test
```

Relevant targets are also available individually:

```bash
make go-test
make go-vet
make web-typecheck
make web-lint
make web-test
make web-build
```

Contract changes must also remain parseable by the `Contracts / validate` GitHub Actions check.

## Code Review Rules

Focus review comments on correctness, contract drift, security, data integrity, and operational safety. Do not report formatting, lint, type-check, or other issues already reliably enforced by CI unless they reveal a behavioral defect.

### Contract boundaries

- Flag implementation or consumer behavior that diverges from the applicable Git SSOT.
- Flag changes that introduce undocumented API fields, states, protocol messages, or semantics without updating the owning contract.
- When a contract changes, check the affected producer and consumer sides instead of reviewing the contract file in isolation.

### Durable operations and provider reconciliation

- Provision, Reset, and Cleanup are durable asynchronous Operations. Flag changes that turn them into request-lifetime synchronous work or bypass the existing Operation/idempotency model.
- If a Provider operation has an unknown outcome, reconciliation must happen before blindly repeating the same Create/Delete operation.

### Reset reproducibility

- Reset must reproduce the instance from its immutable CreationSnapshot/generation baseline. Flag code that rebuilds an existing instance from the current mutable LabSpec.

### Terminal and Live

- Do not persist terminal input/output, transcripts, or Live subscriber queue contents. Persistence is limited to the required lifecycle metadata.
- Preserve the control/data separation for Terminal/Live traffic.
- Live access is read-only; flag changes that allow Live viewers to write to the terminal session.

### Sensitive data and logging

- Flag credentials, secrets, passwords, session secrets, Connector credentials, or raw Provider-sensitive payloads being committed, logged, or exposed through APIs.
- Logs should preserve useful correlation without leaking sensitive values.

### Runtime boundaries

- Keep application-facing contracts independent from internal infrastructure choices where the existing runtime contract requires that boundary.
- Flag changes that leak implementation-specific infrastructure details into external HTTP/WSS contracts without an explicit contract decision.
