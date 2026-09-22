import { describe, expect, it } from 'vitest'

import { resolveLabbitApiMode } from './apiMode'

describe('resolveLabbitApiMode', () => {
  it('개발환경에서 별도 설정이 없으면 Mock을 사용한다', () => {
    expect(resolveLabbitApiMode(true)).toBe('mock')
  })

  it('개발환경에서 http를 선택하면 실제 HTTP consumer를 사용한다', () => {
    expect(resolveLabbitApiMode(true, 'http')).toBe('http')
  })

  it('개발환경의 mode 값은 공백과 대소문자를 정규화한다', () => {
    expect(resolveLabbitApiMode(true, ' HTTP ')).toBe('http')
  })

  it('개발환경의 알 수 없는 mode는 조용히 fallback하지 않는다', () => {
    expect(() => resolveLabbitApiMode(true, 'invalid')).toThrow(
      '지원하지 않는 VITE_LABBIT_API_MODE 값입니다',
    )
  })

  it('production build에서는 설정과 무관하게 HTTP를 사용한다', () => {
    expect(resolveLabbitApiMode(false, 'mock')).toBe('http')
  })
})
