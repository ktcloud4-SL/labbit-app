export type LoginReason = 'authRequired' | 'sessionExpired'

export interface LoginLocationState {
  from?: string
  reason?: LoginReason
  signedOut?: boolean
}

export function isSafeInternalPath(path: string | undefined): path is string {
  return Boolean(path && path.startsWith('/') && !path.startsWith('//'))
}
