/**
 * Build-time guard for the PUBLIC Connector installer URL.
 * The Vite bundle exposes VITE_* values to the browser. Never include secrets.
 * Empty keeps unconfigured builds on the safe, unconfirmed-address UI.
 */
import { pathToFileURL } from 'node:url'
import { validateInstallerBuildEnv } from '../src/shared/installer/installerUrl.mjs'

export { validateInstallerBuildEnv }

// Docker builder uses this entrypoint before Vite can embed any configuration.
// Log only a generic error; do not echo potentially confidential inputs.
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    validateInstallerBuildEnv(process.env.VITE_LABBIT_APPROVED_INSTALLER_URL ?? '')
  } catch {
    console.error('Invalid public VITE_LABBIT_APPROVED_INSTALLER_URL; refusing Web build')
    process.exitCode = 1
  }
}
