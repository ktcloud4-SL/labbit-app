import { spawnSync } from 'node:child_process'
import { join, resolve } from 'node:path'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { describe, expect, it } from 'vitest'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { ConnectorInstallGuide } from '../src/pages/ConnectorInstallGuide'
import { validateInstallerBuildEnv } from './validate-installer-build-env.mjs'

const script = resolve(process.cwd(), 'scripts/validate-installer-build-env.mjs')
const fixture = 'https://installer.example.test/connectors/install.sh'

describe('LBT-183 public Installer URL build-time safety', () => {
  it.each(['', fixture,
    'https://raw.githubusercontent.com/ktcloud4-SL/labbit-app/main/scripts/installer/install.sh',
    'https://raw.githubusercontent.com/ktcloud4-SL/labbit-app/e0caea4f3191e9109171c02581d498f48e4c9887/scripts/installer/install.sh'])(
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
    '\u0001https://installer.example.test/install.sh',
    'https://installer.example.test/\u0001install.sh',
    'https://installer.example.test/\u007finstall.sh',
    'https://installer.example.test/\u0085install.sh',
    'https:installer.example.test/install.sh',
    'https:/installer.example.test/install.sh',
    'https://installer.example.test\\install.sh',
    'https://a..b/install.sh',
    'https://-installer.example.test/install.sh',
    'https://installer-.example.test/install.sh',
    'https://installer.example.test/install.sh?',
    'https://installer.example.test/install.sh#',
    'https://@installer.example.test/install.sh',
    'https://:@installer.example.test/install.sh',
  ])('rejects unsafe or malformed installer URLs before building', (value) => {
    expect(() => validateInstallerBuildEnv(value)).toThrow()
    const markup = renderToStaticMarkup(createElement(ConnectorInstallGuide, { approvedInstallerUrl: value }))
    expect(markup).not.toContain('Installer 주소 복사')
    expect(markup).toContain('공식 Installer 배포 주소가 아직 확정되지 않았습니다')
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

  it('the Docker entrypoint accepts an empty config for unconfigured builds', () => {
    const result = spawnSync(process.execPath, [script], {
      env: { ...process.env, VITE_LABBIT_APPROVED_INSTALLER_URL: '' },
      encoding: 'utf8',
    })
    expect(result.status).toBe(0)
  })

  it.each(['process', 'mode-file', 'explicit-root'])('Vite also rejects invalid final %s config without echoing it', (source) => {
    const temp = mkdtempSync(join(tmpdir(), 'labbit-installer-env-'))
    const raw = 'https://installer.example.test/install.sh?token=synthetic-vite-fixture'
    const env = { ...process.env }
    delete env.VITE_LABBIT_APPROVED_INSTALLER_URL
    try {
      if (source === 'process') env.VITE_LABBIT_APPROVED_INSTALLER_URL = raw
      else writeFileSync(join(temp, '.env.production'), `VITE_LABBIT_APPROVED_INSTALLER_URL=${raw}\n`)
      const result = spawnSync(process.execPath, [
        resolve(process.cwd(), 'node_modules/vite/bin/vite.js'), 'build',
        ...(source === 'explicit-root' ? [temp] : []),
        '--config', resolve(process.cwd(), 'vite.config.ts'),
      ], { cwd: source === 'explicit-root' ? process.cwd() : temp, env, encoding: 'utf8' })
      const output = result.stdout + result.stderr
      expect(result.status).toBe(1)
      expect(output).toContain('Invalid public VITE_LABBIT_APPROVED_INSTALLER_URL')
      expect(output).not.toContain(raw)
      expect(output).not.toContain('synthetic-vite-fixture')
    } finally {
      rmSync(temp, { recursive: true, force: true })
    }
  })
})
