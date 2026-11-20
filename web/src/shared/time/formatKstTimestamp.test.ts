import { describe, expect, it } from 'vitest'

import { formatKstTimestamp } from './formatKstTimestamp'

describe('formatKstTimestamp', () => {
  it('UTC 자정을 넘는 서버 관측 시각을 KST로 표시한다', () => {
    expect(formatKstTimestamp('2026-11-09T23:38:22.456589Z')).toBe(
      '2026-11-10 08:38:22 KST',
    )
  })

  it('명시적 timezone offset도 같은 시각으로 표시한다', () => {
    expect(formatKstTimestamp('2026-11-10T08:38:22+09:00')).toBe(
      '2026-11-10 08:38:22 KST',
    )
  })

  it('다른 offset으로 전년도·다음 연도로 넘어가는 시각을 한국 연도로 정확히 표시한다', () => {
    expect(formatKstTimestamp('2026-01-01T00:00:00+14:00')).toBe(
      '2025-12-31 19:00:00 KST',
    )
    expect(formatKstTimestamp('2026-12-31T23:59:59-12:00')).toBe(
      '2027-01-01 20:59:59 KST',
    )
  })

  it('잘못된 시간대 offset은 날짜처럼 정규화하거나 UTC로 간주하지 않는다', () => {
    for (const invalid of [
      '2026-11-10T08:38:22+24:00',
      '2026-11-10T08:38:22+09:99',
      '2026-11-10T08:38:22+00:60',
    ]) {
      expect(formatKstTimestamp(invalid)).toBe('확인 불가')
    }
  })
  it('서버 시각이 없거나 잘못됐을 때 임의의 현재시각을 만들지 않는다', () => {
    expect(formatKstTimestamp(null)).toBe('확인 불가')
    expect(formatKstTimestamp(undefined)).toBe('확인 불가')
    expect(formatKstTimestamp('')).toBe('확인 불가')
    expect(formatKstTimestamp('invalid')).toBe('확인 불가')
    expect(formatKstTimestamp('2026-13-10T00:00:00Z')).toBe('확인 불가')
  })

  it('존재하지 않는 날짜를 JS가 자동 보정하더라도 유효한 관측시각으로 표시하지 않는다', () => {
    expect(formatKstTimestamp('2026-02-30T08:38:22Z')).toBe('확인 불가')
    expect(formatKstTimestamp('2026-02-29T08:38:22Z')).toBe('확인 불가')
    expect(formatKstTimestamp('2026-04-31T08:38:22Z')).toBe('확인 불가')
    expect(formatKstTimestamp('2026-11-10T24:00:00Z')).toBe('확인 불가')
    expect(formatKstTimestamp('2026-11-10T08:38:60Z')).toBe('확인 불가')
  })

  it('윤년의 2월 29일은 정상적으로 KST에 변환한다', () => {
    expect(formatKstTimestamp('2024-02-29T08:38:22Z')).toBe('2024-02-29 17:38:22 KST')
  })

  it('timezone 정보가 없는 시각은 브라우저 로컬 시각으로 추정하지 않는다', () => {
    expect(formatKstTimestamp('2026-11-10T08:38:22')).toBe('확인 불가')
  })
})
