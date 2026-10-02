import { access, copyFile, mkdir, readFile, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const targetDir = process.argv[2]?.trim()
const pluginId = process.argv[3]?.trim()

if (!targetDir || !pluginId) {
  console.error('사용법: node install-dev-plugin.mjs <Figma plugin folder> <plugin id>')
  console.error('예: node install-dev-plugin.mjs "%USERPROFILE%\\Downloads\\Labbit UX Importer" 1234567890123456789')
  process.exit(1)
}

if (!/^\d+$/.test(pluginId)) {
  console.error('plugin id는 숫자만 입력해 주세요.')
  process.exit(1)
}

async function exists(filePath) {
  try {
    await access(filePath)
    return true
  } catch {
    return false
  }
}

async function main() {
  await mkdir(targetDir, { recursive: true })

  const targetManifest = path.join(targetDir, 'manifest.json')
  const backupManifest = path.join(targetDir, 'manifest.figma-template.backup.json')

  if ((await exists(targetManifest)) && !(await exists(backupManifest))) {
    await copyFile(targetManifest, backupManifest)
    console.log('✓ 기존 Figma template manifest 백업')
  }

  const template = await readFile(path.join(here, 'manifest.template.json'), 'utf8')
  const manifest = template.replace('__FIGMA_PLUGIN_ID__', pluginId)

  await Promise.all([
    writeFile(targetManifest, manifest),
    copyFile(path.join(here, 'code.js'), path.join(targetDir, 'code.js')),
    copyFile(path.join(here, 'ui.html'), path.join(targetDir, 'ui.html')),
  ])

  console.log('✓ Labbit UX Importer 설치 완료')
  console.log(`- 대상: ${targetDir}`)
  console.log('- manifest.json 교체')
  console.log('- code.js 설치')
  console.log('- ui.html 설치')
  console.log('')
  console.log('Figma에서 기존 Labbit UX Importer를 닫았다가 다시 실행하세요.')
}

main().catch((error) => {
  console.error(`\nFigma 개발 Plugin 설치 실패\n${error instanceof Error ? error.message : error}`)
  process.exitCode = 1
})
