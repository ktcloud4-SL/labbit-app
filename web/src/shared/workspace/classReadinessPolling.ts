import type { ClassDetail } from '../api/contracts'
import { HttpError } from '../api/httpClient'

// 준비 상태에서만 Class 정보를 주기적으로 확인한다. READY에 도달하면 불필요한
// 요청을 중단하고, 조회 오류에서는 반복 요청으로 로그인/권한 오류를 숨기지 않는다.
export const CLASS_READINESS_POLL_MS = 5_000
export const CLASS_READINESS_MAX_RETRIES = 2

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


// 최초 조회 실패나 401/403/404는 자동 재시도하지 않는다. 이미 정상 수신한
// 준비 중 Class 데이터가 있을 때에만 일시적 장애에 2회의 복구 기회를 준다.
// TanStack Query는 성공 시 직전 오류를 지우므로 대기 화면을 유지할 수 있다.
export function retryPendingClassReadiness(
  classDetail: ClassDetail | undefined,
  failureCount: number,
  error: unknown,
): boolean {
  if (
    failureCount >= CLASS_READINESS_MAX_RETRIES ||
    classReadinessPollingInterval(classDetail, null) === false
  ) return false

  if (error instanceof HttpError) {
    return error.status >= 500 && error.status < 600
  }

  return error instanceof TypeError
}
