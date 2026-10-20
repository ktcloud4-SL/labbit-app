/**
 * Build-time guard for the PUBLIC Connector installer URL.
 * The Vite bundle exposes VITE_* values to the browser. Never include secrets.
 * Empty means LBT-181 has not yet approved an installer URL (safe default).
 */
import { pathToFileURL } from 'node:url'

export function validateInstallerBuildEnv(raw = '') {
  if (raw === '') return
  if (raw !== raw.trim() || /[\s\\]/u.test(raw)) {
    throw new Error('Approved installer URL must be a single absolute HTTPS URL')
  }

  let url
  try {
    url = new URL(raw)
  } catch {
    throw new Error('Approved installer URL must be a valid HTTPS URL')
  }

  if (url.protocol !== 'https:' || !url.hostname || url.username || url.password ||
      url.search || url.hash || url.pathname === '/') {
    throw new Error('Approved installer URL must use HTTPS without credentials, query or fragment')
  }
}

// Docker builder uses this entrypoint before Vite can embed any configuration.
// Log only a generic error; do not echo potentially confidential inputs.
if (process.argv[1] && import.meta.url === new URL(`file://${process.argv[1]}`).href) {
  try {
    validateInstallerBuildEnv(process.env.VITE_LABBIT_APPROVED_INSTALLER_URL ?? '')
  } catch {
    console.error('Invalid public VITE_LABBIT_APPROVED_INSTALLER_URL; refusing Web build')
    process.exitCode = 1
  }
}
