/** URL shape validation only. Approval and release trust belong to the owner. */
export function normalizeInstallerUrl(raw) {
  if (typeof raw !== 'string' || !/^https:\/\//iu.test(raw) ||
      /[\s\\\p{Cc}]/u.test(raw) || /[?#]/u.test(raw) ||
      /^https:\/\/[^/]*@/iu.test(raw)) return null

  try {
    const url = new URL(raw)
    if (url.protocol !== 'https:' || !url.hostname || url.username || url.password ||
        url.search || url.hash || url.pathname === '/') return null

    // URL parsing accepts DNS-invalid names such as a..b or -host. IPv6
    // literals are already checked by URL; normal DNS names need label checks.
    if (!url.hostname.startsWith('[')) {
      const hostname = url.hostname.replace(/\.$/u, '')
      if (hostname.length > 253 || hostname.split('.').some((label) =>
        !/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/iu.test(label))) return null
    }
    return url.toString()
  } catch {
    return null
  }
}

export function validateInstallerBuildEnv(raw = '') {
  if (raw !== '' && normalizeInstallerUrl(raw) === null) {
    // Never echo an invalid value: VITE_* is public, not a secret channel.
    throw new Error('Invalid public VITE_LABBIT_APPROVED_INSTALLER_URL; refusing Web build')
  }
}
