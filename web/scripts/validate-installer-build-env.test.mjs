import { spawnSync } from 'node:child_process'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { validateInstallerBuildEnv } from './validate-installer-build-env.mjs'

const script = resolve(process.cwd(), 'scripts/validate-installer-build-env.mjs')
const fixture = 'https://installer.example.test/connectors/install.sh'

describe('LBT-183 public Installer URL build-time safety', () => {
  it.each(['', fixture, 'https://raw.githubusercontent.com/ktcloud4-SL/labbit-app/e0caea4f3191e9109171c02581d498f48e4c9887/scripts/installer/install.sh'])(
    'accepts empty or safe public HTTPS installer URL',
    (value) => expect(() => validateInstallerBuildEnv(value)).not.toThrow(),
  )

  it.each([
    'http://installer.example.test/install.sh',
    'https://installer.example.test/install.sh?token=do-not-embed',
    'https://installer.example.test/install.sh#fragment',
    'https://user:secret@installer.example.test/install.sh',
    'https://installer.example.test/',
    'https://installer.example.test/install.sh\nFAKE_SECRET',
    'not-a-url',
  ])('rejects unsafe or malformed installer URLs before building', (value) => {
    expect(() => validateInstallerBuildEnv(value)).toThrow()
  })

  it('the Docker entrypoint rejects an unsafe build env without echoing it', () => {
    const raw = 'https://installer.example.test/install.sh?token=private-test-fixture'
    const result = spawnSync(process.execPath, [script], {
      env: { ...process.env, VITE_LABBIT_APPROVED_INSTALLER_URL: raw },
      encoding: 'utf8',
    })
    expect(result.status).toBe(1)
    expect(result.stderr).toContain('Invalid public VITE_LABBIT_APPROVED_INSTALLER_URL')
    expect(result.stderr).not.toContain(raw)
    expect(result.stderr).not.toContain('private-test-fixture')
  })

  it('the Docker entrypoint accepts an empty config while release approval is pending', () => {
    const result = spawnSync(process.execPath, [script], {
      env: { ...process.env, VITE_LABBIT_APPROVED_INSTALLER_URL: '' },
      encoding: 'utf8',
    })
    expect(result.status).toBe(0)
  })
})
