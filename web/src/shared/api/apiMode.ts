export type LabbitApiMode = 'mock' | 'http'

export function resolveLabbitApiMode(
  isDev: boolean,
  configuredMode?: string,
): LabbitApiMode {
  if (!isDev) {
    return 'http'
  }

  const mode = configuredMode?.trim().toLowerCase()

  if (!mode || mode === 'mock') {
    return 'mock'
  }

  if (mode === 'http') {
    return 'http'
  }

  throw new Error(
    `지원하지 않는 VITE_LABBIT_API_MODE 값입니다: ${configuredMode}`,
  )
}
