import { describe, expect, it } from 'vitest'

import type { ClassDetail } from '../api/contracts'
import {
  CLASS_READINESS_POLL_MS,
  classReadinessPollingInterval,
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
})
