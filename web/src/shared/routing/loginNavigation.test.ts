import { describe, expect, it } from 'vitest'

import { isSafeInternalPath } from './loginNavigation'

describe('isSafeInternalPath', () => {
  it('same-origin 내부 path와 query를 허용한다', () => {
    expect(isSafeInternalPath('/classes/class-a?tab=workspace')).toBe(true)
  })

  it('protocol-relative·absolute URL과 빈 값은 거부한다', () => {
    expect(isSafeInternalPath('//evil.example/path')).toBe(false)
    expect(isSafeInternalPath('https://evil.example/path')).toBe(false)
    expect(isSafeInternalPath(undefined)).toBe(false)
  })
})
