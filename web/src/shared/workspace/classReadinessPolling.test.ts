import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'

import type { ClassDetail } from '../api/contracts'
import { HttpError } from '../api/httpClient'
import { labbitQueryKeys } from '../api/labbitApi'
import {
  CLASS_READINESS_POLL_MS,
  CLASS_READINESS_MAX_RETRIES,
  classReadinessPollingInterval,
  retryPendingClassReadiness,
} from './classReadinessPolling'

const pendingClass: ClassDetail = {
  id: 'class-a',
  name: 'Lab Class',
  myRole: 'STUDENT',
  activeLabExecution: { id: 'execution-a', status: 'PROVISIONING' },
  myLabInstance: {
    id: 'lab-instance-a',
    userId: 'student-a',
    status: 'PENDING',
    generation: 1,
  },
}

describe('classReadinessPollingInterval', () => {
  it.each(['PENDING', 'PROVISIONING', 'DELETING'])(
    '%s 상태에서는 Class를 5초마다 조회한다',
    (status) => {
      expect(
        classReadinessPollingInterval(
          { ...pendingClass, myLabInstance: { ...pendingClass.myLabInstance!, status } },
          null,
        ),
      ).toBe(CLASS_READINESS_POLL_MS)
    },
  )

  it('실습 실행 중이지만 아직 인스턴스가 배정되지 않은 학생도 상태를 확인한다', () => {
    expect(
      classReadinessPollingInterval(
        { ...pendingClass, myLabInstance: undefined },
        null,
      ),
    ).toBe(CLASS_READINESS_POLL_MS)
  })

  it('활성화된 실습에서 대상자로 선택되지 않은 학생은 계속 조회하지 않는다', () => {
    expect(
      classReadinessPollingInterval(
        {
          ...pendingClass,
          activeLabExecution: { id: 'execution-a', status: 'ACTIVE' },
          myLabInstance: undefined,
        },
        null,
      ),
    ).toBe(false)
  })

  it('실습 생성 이후 정리 단계의 미할당 학생도 계속 조회하지 않는다', () => {
    expect(
      classReadinessPollingInterval(
        {
          ...pendingClass,
          activeLabExecution: { id: 'execution-a', status: 'CLEANING_UP' },
          myLabInstance: undefined,
        },
        null,
      ),
    ).toBe(false)
  })

  it.each(['READY', 'ERROR', 'COMPLETED', 'UNKNOWN'])(
    '%s 상태에서는 주기 조회를 멈춘다',
    (status) => {
      expect(
        classReadinessPollingInterval(
          { ...pendingClass, myLabInstance: { ...pendingClass.myLabInstance!, status } },
          null,
        ),
      ).toBe(false)
    },
  )

  it('진행 중인 LabExecution이 없으면 불필요한 조회를 하지 않는다', () => {
    expect(
      classReadinessPollingInterval(
        { ...pendingClass, activeLabExecution: undefined },
        null,
      ),
    ).toBe(false)
  })

  it('조회 데이터가 없거나 오류가 발생하면 반복 요청을 하지 않는다', () => {
    expect(classReadinessPollingInterval(undefined, null)).toBe(false)
    expect(classReadinessPollingInterval(pendingClass, new Error('network'))).toBe(false)
  })
  it('준비 중 상태의 일시적 5xx·네트워크 오류는 최대 2회 재시도한다', () => {
    expect(CLASS_READINESS_MAX_RETRIES).toBe(2)
    expect(retryPendingClassReadiness(pendingClass, 0, new HttpError(503))).toBe(true)
    expect(retryPendingClassReadiness(pendingClass, 1, new HttpError(502))).toBe(true)
    expect(retryPendingClassReadiness(pendingClass, 2, new HttpError(503))).toBe(false)
    expect(retryPendingClassReadiness(pendingClass, 0, new TypeError('Failed to fetch'))).toBe(true)
  })

  it.each([400, 401, 403, 404, 422])(
    'HTTP %s는 정상 준비 중 데이터가 있어도 재시도하지 않는다',
    (status) => {
      expect(retryPendingClassReadiness(pendingClass, 0, new HttpError(status))).toBe(false)
    },
  )

  it('미조회 상태 또는 준비가 끝난 상태에는 재시도하지 않는다', () => {
    const readyClass: ClassDetail = {
      ...pendingClass,
      myLabInstance: { ...pendingClass.myLabInstance!, status: 'READY' },
    }
    expect(retryPendingClassReadiness(undefined, 0, new HttpError(503))).toBe(false)
    expect(retryPendingClassReadiness(readyClass, 0, new HttpError(503))).toBe(false)
    expect(retryPendingClassReadiness(pendingClass, 0, new Error('unexpected'))).toBe(false)
  })

  it('PENDING → 일시적 503 → READY: TanStack Query가 마지막 정상 데이터로 복구한다', async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const key = labbitQueryKeys.classDetail('class-a')
    client.setQueryData(key, pendingClass)

    const readyClass: ClassDetail = {
      ...pendingClass,
      myLabInstance: { ...pendingClass.myLabInstance!, status: 'READY' },
    }
    const getClass = vi.fn()
      .mockRejectedValueOnce(new HttpError(503))
      .mockResolvedValueOnce(readyClass)

    const result = await client.fetchQuery({
      queryKey: key,
      queryFn: getClass,
      staleTime: 0,
      retryDelay: 0,
      retry: (failureCount, error) =>
        retryPendingClassReadiness(
          client.getQueryData<ClassDetail>(key),
          failureCount,
          error,
        ),
    })

    expect(getClass).toHaveBeenCalledTimes(2)
    expect(result.myLabInstance?.status).toBe('READY')
    expect(client.getQueryData<ClassDetail>(key)?.myLabInstance?.status).toBe('READY')
    expect(classReadinessPollingInterval(result, null)).toBe(false)
    expect(client.getQueryState(key)?.error).toBeNull()
    client.clear()
  })
})
