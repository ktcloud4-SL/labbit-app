# 전체 PR diff 파일 검토

최신 main 기준 코드/설정/테스트/이전 Evidence와 이번 새 Evidence를 포함한다. 이전 감사의 파일별 설명에 현재 call boundary와 추가 종료/회귀 검토를 합쳤다. 아래 주요 라인은 현재 파일의 함수/시험이며 line range가 아니다. 새 Evidence는 최종 publication 검사에서 다시 검토한다.

| 파일 | 목적·영향·검토 근거 | 주요 라인 |
| --- | --- | --- |
| docs/connector/evidence/LBT-147/2026-10-10/README.md | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-10/artifact-checksums.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-10/automated-logs.zip | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-10/reproduce.ps1 | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-10/secret-scan.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-10/source-manifest.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-10/validation-summary.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-additional-verification/.gitattributes | 이번 감사/fixture/재현/artifact 게시 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-additional-verification/.gitignore | 이번 감사/fixture/재현/artifact 게시 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-additional-verification/AUDIT.md | 이번 감사/fixture/재현/artifact 게시 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-additional-verification/README.md | 이번 감사/fixture/재현/artifact 게시 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-additional-verification/_fixtures/actual_runtime_shutdown_test.go | 이번 감사/fixture/재현/artifact 게시 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-additional-verification/_fixtures/integration_metadata_test.go | 이번 감사/fixture/재현/artifact 게시 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-additional-verification/_fixtures/read_only_residual_test.go | 이번 감사/fixture/재현/artifact 게시 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-additional-verification/_fixtures/real_failure_test.go | 이번 감사/fixture/재현/artifact 게시 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-additional-verification/archive-manifest.json | 이번 감사/fixture/재현/artifact 게시 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-additional-verification/artifact-checksums.json | 이번 감사/fixture/재현/artifact 게시 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-additional-verification/audit-logs.zip | 이번 감사/fixture/재현/artifact 게시 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-additional-verification/file-review.md | 이번 감사/fixture/재현/artifact 게시 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-additional-verification/independent-review.md | 이번 감사/fixture/재현/artifact 게시 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-additional-verification/prepare_overlays.py | 이번 감사/fixture/재현/artifact 게시 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-additional-verification/publication-checks.json | 이번 감사/fixture/재현/artifact 게시 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-additional-verification/publication-review.md | 이번 감사/fixture/재현/artifact 게시 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-additional-verification/reproduction-checks.json | 이번 감사/fixture/재현/artifact 게시 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-additional-verification/secret-scan.json | 이번 감사/fixture/재현/artifact 게시 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-additional-verification/source-manifest.json | 이번 감사/fixture/재현/artifact 게시 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-additional-verification/ssot-summary.json | 이번 감사/fixture/재현/artifact 게시 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-additional-verification/validation-summary.json | 이번 감사/fixture/재현/artifact 게시 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/.gitattributes | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/.gitignore | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/AUDIT.md | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/README.md | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/_fixtures/LICENSE-x-crypto.txt | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/_fixtures/dependency-fixture-manifest.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/_fixtures/independent_unacked_setup_test.go | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/_fixtures/reproduce-independent-unacked.ps1 | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/_fixtures/reviewer-executed-source-manifest.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/_fixtures/ssh-channel-no-b-close-ack.go | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/archive-manifest.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/artifact-checksums.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/audit-logs.zip | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/file-review.md | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/independent-review.md | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/publication-checks.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/publication-review.md | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/reproduce-guard-regression.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/reproduce.py | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/secret-scan.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/source-manifest.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/ssot-summary.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12-review-followup/validation-summary.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12/.gitattributes | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12/AUDIT.md | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12/README.md | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12/archive-manifest.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12/artifact-checksums.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12/audit-logs.zip | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12/automated-validation-summary.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12/post-discovery-and-residual.jsonl | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12/pre-discovery.jsonl | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12/publication-checks.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12/publication-review.md | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12/real-app-boundaries.jsonl | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12/real-vm-access.jsonl | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12/reproduce-guard-regression.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12/reproduce.ps1 | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12/secret-scan.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12/source-manifest.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12/ssot-summary.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12/validation-summary.json | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| docs/connector/evidence/LBT-147/2026-10-12/verify-reproduce-guards.ps1 | 과거 source/result·skip/초기실패·archive/hash·재현 의존성 보존 검토 | artifact |
| internal/connector/app/app.go | production wiring·lazy Provider cancellation/cache·partial generation fence·Terminal/SSH/Control shutdown | 208: func Run(ctx context.Context) error |
| internal/connector/app/app_test.go | production wiring·lazy Provider cancellation/cache·partial generation fence·Terminal/SSH/Control shutdown | 23: func TestControlConfigFromEnvironment_ValidatesControlInputs(t *testing.T)<br>46: func TestRunControl_KeepsWSSConnectedWhenProviderAuthenticationFails(t *testing.T)<br>125: func TestBuildConnector_ProductionTerminalWiring(t *testing.T)<br>184: func TestConnectorApp_GracefulShutdown(t *testing.T)<br>244: func TestBuildConnector_ProductionContract_Validation(t *testing.T) |
| internal/connector/app/backpressure_test.go | 실제 TCP unread peer·종료/교체/분리/복구·exact-once 및 자원해제 회귀 | 34: func TestRunControlShutdownWithUnreadDataPeer(t *testing.T) |
| internal/connector/app/main_integration_test.go | production wiring·lazy Provider cancellation/cache·partial generation fence·Terminal/SSH/Control shutdown | 32: func (p *pinnedTestProvider) ResolveVMAccess(ctx context.Context, target vmaccess.Target) (vmaccess.Endpoint, error)<br>50: func TestLatestMainControlProviderAndTerminalShareRuntime(t *testing.T) |
| internal/connector/app/runtime_provider.go | production wiring·lazy Provider cancellation/cache·partial generation fence·Terminal/SSH/Control shutdown | 140: func (p *lazyProvider) ResolveVMAccess(ctx context.Context, target vmaccess.Target) (vmaccess.Endpoint, error) |
| internal/connector/app/runtime_provider_test.go | production wiring·lazy Provider cancellation/cache·partial generation fence·Terminal/SSH/Control shutdown | 15: func TestLazyProvider_RetriesInitializationAfterFailure(t *testing.T)<br>45: func TestLazyProvider_AuthenticationFailureIsDefiniteBeforeMutation(t *testing.T)<br>102: func (p *lazyAccessTestProvider) ResolveVMAccess(context.Context, vmaccess.Target) (vmaccess.Endpoint, error)<br>107: func TestLazyProvider_WaitingVMResolutionHonorsContext(t *testing.T)<br>174: func TestLazyProvider_VMSetupTimeoutWhileInitializationIsPending(t *testing.T) |
| internal/connector/app/vm_access.go | production wiring·lazy Provider cancellation/cache·partial generation fence·Terminal/SSH/Control shutdown | 전체 파일 |
| internal/connector/app/vm_access_lifecycle_test.go | production wiring·lazy Provider cancellation/cache·partial generation fence·Terminal/SSH/Control shutdown | 25: func (r lifecycleResolver) ResolveVMAccess(ctx context.Context, target vmaccess.Target) (vmaccess.Endpoint, error)<br>201: func TestFailedLifecycleClosesOldChannelsAndPreservesPreflight(t *testing.T)<br>259: func TestFailedLifecycleRejectsDelayedOldResolutionAndSetup(t *testing.T) |
| internal/connector/app/vm_access_test.go | production wiring·lazy Provider cancellation/cache·partial generation fence·Terminal/SSH/Control shutdown | 15: func (r *rejectedAccessResolver) ResolveVMAccess(context.Context, vmaccess.Target) (vmaccess.Endpoint, error)<br>20: func TestOperationResultInvalidatesObsoleteAccess(t *testing.T)<br>73: func TestSSHIdleRuntimeDuration(t *testing.T) |
| internal/connector/provider/openstack/lifecycle_test.go | strict target/management endpoint·pin·invalid/unavailable 분류·partial resource cleanup provenance | 12: func TestCleanupDeletesExactIDsInDependencyOrderAndTreats404AsDeleted(t *testing.T)<br>64: func TestCleanupPartialDeleteRejectionReportsDestructiveFailure(t *testing.T)<br>103: func TestCleanupDetachesRouterInterfaceBeforeDelete(t *testing.T)<br>141: func TestCleanupUncertainDeleteStopsAndPreservesEveryTarget(t *testing.T)<br>167: func TestCleanupRequestTimeoutRemainsUnknown(t *testing.T) |
| internal/connector/provider/openstack/terminal_error_test.go | strict target/management endpoint·pin·invalid/unavailable 분류·partial resource cleanup provenance | 20: func TestVMAccessProviderFailuresAndTargetMismatchControlCodes(t *testing.T) |
| internal/connector/provider/openstack/terminal_target.go | strict target/management endpoint·pin·invalid/unavailable 분류·partial resource cleanup provenance | 31: func (a *Adapter) ResolveVMAccess(ctx context.Context, target vmaccess.Target) (vmaccess.Endpoint, error) |
| internal/connector/provider/openstack/terminal_target_test.go | strict target/management endpoint·pin·invalid/unavailable 분류·partial resource cleanup provenance | 17: func TestTerminalTargetRejectsInvalidInputsBeforeCloudLookup(t *testing.T)<br>48: func TestVMAccessRejectsMismatchedEnvelopeBeforeManagementLookup(t *testing.T)<br>76: func TestResolveServerAddressManagementOnly(t *testing.T)<br>137: func TestTerminalHostKeyUsesExistingServerPin(t *testing.T) |
| internal/connector/provider/openstack/vm_access_integration_test.go | strict target/management endpoint·pin·invalid/unavailable 분류·partial resource cleanup provenance | 21: func TestOpenStackVMAccessIntegration(t *testing.T)<br>175: func realOpenSession(t *testing.T, ctx context.Context, m *vmaccess.Manager, target vmaccess.Target) *vmaccess.Session |
| internal/connector/terminal/backpressure_test.go | 실제 TCP unread peer·종료/교체/분리/복구·exact-once 및 자원해제 회귀 | 58: func (*streamingDataPTY) Resize(int, int) error<br>59: func (p *streamingDataPTY) Close() error<br>64: func TestSessionShutdownWithUnreadDataPeer(t *testing.T) |
| internal/connector/terminal/data_connection_backpressure_test.go | 실제 TCP unread peer·종료/교체/분리/복구·exact-once 및 자원해제 회귀 | 59: func TestSessionReleasesUnreadDataConnectionOnReplaceAndDetach(t *testing.T)<br>135: func TestSessionRecoversFromUnreadDataConnectionWithoutEndingPTY(t *testing.T) |
| internal/connector/terminal/final_frame_backpressure_test.go | 실제 TCP unread peer·종료/교체/분리/복구·exact-once 및 자원해제 회귀 | 39: func TestSessionBoundsFinalDataEndedWrite(t *testing.T) |
| internal/connector/terminal/session.go | 관리 SSH PTY 채널 소유권·ENDED·grace·네트워크 대기와 state mutex 분리·Resize 직렬화 | 198: func (sm *SessionManager) Shutdown(reason string)<br>340: func (s *Session) Close(reason string, exitCode *int, err error)<br>447: func (s *Session) Resize(cols, rows int) error |
| internal/connector/terminal/shutdown_test.go | 관리 SSH PTY 채널 소유권·ENDED·grace·네트워크 대기와 state mutex 분리·Resize 직렬화 | 14: func TestTerminalShutdownRejectsDelayedOpensAndEndsOnce(t *testing.T) |
| internal/connector/terminal/ssh_backpressure_test.go | 실제 TCP unread peer·종료/교체/분리/복구·exact-once 및 자원해제 회귀 | 146: func TestSSHPTYCloseWithUnreadPeer(t *testing.T)<br>172: func TestSessionShutdownWithUnreadSSHResize(t *testing.T) |
| internal/connector/terminal/ssh_manager_backpressure_test.go | 실제 TCP unread peer·종료/교체/분리/복구·exact-once 및 자원해제 회귀 | 23: func (r unreadManagerSSHResolver) ResolveVMAccess(context.Context, vmaccess.Target) (vmaccess.Endpoint, error)<br>30: func TestManagedSSHShutdownWithUnreadPeer(t *testing.T) |
| internal/connector/terminal/ssh_pty.go | 관리 SSH PTY 채널 소유권·ENDED·grace·네트워크 대기와 state mutex 분리·Resize 직렬화 | 165: func (p *SSHPTY) Resize(cols, rows int) error<br>183: func (p *SSHPTY) Close() error |
| internal/connector/terminal/terminal_test.go | 관리 SSH PTY 채널 소유권·ENDED·grace·네트워크 대기와 state mutex 분리·Resize 직렬화 | 32: func (p *resizeObservedPTY) Resize(cols, rows int) error<br>40: func TestTerminal_OpenAndEchoStreaming(t *testing.T)<br>109: func TestTerminal_Resize(t *testing.T)<br>159: func TestTerminal_GracePeriod_Resume(t *testing.T)<br>234: func TestTerminal_GracePeriod_Timeout(t *testing.T) |
| internal/connector/vmaccess/access.go | 공유 pinned SSH·독립 채널·idle·bounded cancellation/late result·generation fencing | 86: func (m *Manager) OpenSession(ctx context.Context, target Target) (*Session, error)<br>266: func (m *Manager) Close() error<br>286: func (s *Session) Close() error<br>297: func (s *Session) Setup(ctx context.Context, setup func(*ssh.Session) error) error |
| internal/connector/vmaccess/access_test.go | 공유 pinned SSH·독립 채널·idle·bounded cancellation/late result·generation fencing | 25: func (f resolverFunc) ResolveVMAccess(ctx context.Context, target Target) (Endpoint, error)<br>212: func TestSharedTransportIndependentChannelsAndIdleExpiry(t *testing.T)<br>235: func TestConcurrentChannelsShareOneHandshake(t *testing.T)<br>261: func TestGenerationAndTargetChangeRetireOldTransport(t *testing.T)<br>290: func TestDisconnectEvictsAndDoesNotReconnectUntilRequested(t *testing.T) |
| internal/connector/vmaccess/cancellation_test.go | 공유 pinned SSH·독립 채널·idle·bounded cancellation/late result·generation fencing | 21: func TestSocketDeadlinePrecedesContextTimer(t *testing.T)<br>33: func TestHandshakeTimeoutAndCancellationCloseSocket(t *testing.T)<br>84: func TestResetDuringChannelSetupRejectsLateSuccess(t *testing.T)<br>101: func TestResetDuringResolutionRejectsLateTargetBeforeDial(t *testing.T) |
| internal/connector/vmaccess/channel_isolation_test.go | 공유 pinned SSH·독립 채널·idle·bounded cancellation/late result·generation fencing | 14: func TestCanceledChannelLeavesActivePTYUsable(t *testing.T)<br>83: func TestCanceledOpenClosesLateChannelAndReusesTransport(t *testing.T)<br>134: func TestStalledOpenDoesNotAccumulateCanceledSSHRequests(t *testing.T) |
| internal/connector/wss/handler.go | authenticated envelope→target·safe error enums·normal reconnect vs process lifecycle hook | 1117: func (h *Handler) handleTerminalClose(ctx context.Context, env protocol.BaseEnvelope, raw []byte) error |
| internal/connector/wss/reconnect.go | authenticated envelope→target·safe error enums·normal reconnect vs process lifecycle hook | 122: func (s *Supervisor) SetOnShutdown(cb func())<br>137: func (s *Supervisor) Run(ctx context.Context) error |
| internal/connector/wss/reconnect_test.go | authenticated envelope→target·safe error enums·normal reconnect vs process lifecycle hook | 19: func TestBackoffPolicy_CalculationAndJitter(t *testing.T)<br>129: func (r *ReconnectableMockServer) Close()<br>140: func TestSupervisor_LifecycleAndReconnect(t *testing.T)<br>252: func TestSupervisor_ContextCancelBeforeDial(t *testing.T)<br>269: func TestSupervisor_BackoffResetAfterSuccessfulConnection(t *testing.T) |
| internal/connector/wss/vm_access_test.go | authenticated envelope→target·safe error enums·normal reconnect vs process lifecycle hook | 17: func TestTerminalControlRedactsRawSSHFailures(t *testing.T) |
| internal/observability/tracing/runtime_test.go | 승인된 테스트 판정만: deadline 오류·상한·export_timeout 유지; production 변경 없음 | 33: func TestNoneModeCreatesSpansWithoutExporter(t *testing.T)<br>59: func TestNoneModeContinuesRemoteParentAndKeepsSampledFlag(t *testing.T)<br>101: func TestRuntimeResourceIdentifiesServiceEnvironmentAndComponent(t *testing.T)<br>130: func TestOTLPHTTPProtobufExportsSpansToCollector(t *testing.T)<br>188: func TestOTLPGRPCExportsSpansToCollector(t *testing.T) |
| runtime/README.md | owning runtime0.1.7 idle1m positive duration; Terminal grace 독립 | 전체 파일 |
| runtime/contract.yaml | owning runtime0.1.7 idle1m positive duration; Terminal grace 독립 | 전체 파일 |
