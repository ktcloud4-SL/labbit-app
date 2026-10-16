import type { ClassDetail } from '../api/contracts'

// 준비 상태에서만 Class 정보를 주기적으로 확인한다. READY에 도달하면 불필요한
// 요청을 중단하고, 조회 오류에서는 반복 요청으로 로그인/권한 오류를 숨기지 않는다.
export const CLASS_READINESS_POLL_MS = 5_000

export function classReadinessPollingInterval(
  classDetail: ClassDetail | undefined,
  error: unknown,
): number | false {
  if (error || !classDetail?.activeLabExecution) return false

  const status = classDetail.myLabInstance?.status
  return !status ||
    status === 'PENDING' ||
    status === 'PROVISIONING' ||
    status === 'DELETING'
    ? CLASS_READINESS_POLL_MS
    : false
}
