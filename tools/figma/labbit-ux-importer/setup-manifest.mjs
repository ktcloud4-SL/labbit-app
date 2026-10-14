import { readFile, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const pluginId = (process.argv[2] ?? process.env.FIGMA_PLUGIN_ID ?? '').trim()

if (!/^\d+$/.test(pluginId)) {
  console.error('사용법: node setup-manifest.mjs <Figma plugin id>')
  console.error('예: node setup-manifest.mjs 1234567890123456789')
  process.exit(1)
}

const template = await readFile(path.join(here, 'manifest.template.json'), 'utf8')
const manifest = template.replace('__FIGMA_PLUGIN_ID__', pluginId)

await writeFile(path.join(here, 'manifest.json'), manifest)
console.log('✓ manifest.json 생성 완료')
console.log('Figma > Plugins > Development > Import new plugin from manifest... 에서 이 파일을 선택하세요.')
