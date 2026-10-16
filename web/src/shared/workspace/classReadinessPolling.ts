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
  if (!status) {
    // 선택되지 않은 학생에게는 LabInstance가 없을 수 있다.
    // 실습 생성이 끝난 뒤에는 미할당 상태를 무한 조회하지 않는다.
    return classDetail.activeLabExecution.status === 'PROVISIONING'
      ? CLASS_READINESS_POLL_MS
      : false
  }

  return status === 'PENDING' ||
    status === 'PROVISIONING' ||
    status === 'DELETING'
    ? CLASS_READINESS_POLL_MS
    : false
}
