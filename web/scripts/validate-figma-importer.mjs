import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { spawnSync } from 'node:child_process'

const root = path.resolve('..')
const pluginDir = path.join(root, 'tools', 'figma', 'labbit-ux-importer')

async function read(name) {
  return readFile(path.join(pluginDir, name), 'utf8')
}

function requireText(content, needle, label) {
  if (!content.includes(needle)) {
    throw new Error(`${label}: 필요한 문자열이 없습니다: ${needle}`)
  }
}

function forbidText(content, needle, label) {
  if (content.includes(needle)) {
    throw new Error(`${label}: 사용하면 안 되는 문자열이 있습니다: ${needle}`)
  }
}

async function main() {
  const [manifestRaw, code, ui] = await Promise.all([
    read('manifest.template.json'),
    read('code.js'),
    read('ui.html'),
  ])

  const manifest = JSON.parse(manifestRaw)
  const [captureSource, bundleSource] = await Promise.all([
    readFile(path.join('scripts', 'capture-ui.mjs'), 'utf8'),
    readFile(path.join('scripts', 'prepare-figma-bundle.mjs'), 'utf8'),
  ])
  if (manifest.name !== 'Labbit UX Importer') {
    throw new Error('manifest: plugin name 불일치')
  }
  if (manifest.id !== '__FIGMA_PLUGIN_ID__') {
    throw new Error('manifest: 개발용 ID placeholder가 변경되었습니다.')
  }
  if (manifest.documentAccess !== 'dynamic-page') {
    throw new Error('manifest: documentAccess는 dynamic-page여야 합니다.')
  }
  if (!Array.isArray(manifest.editorType) || !manifest.editorType.includes('figma')) {
    throw new Error('manifest: editorType에 figma가 필요합니다.')
  }

  requireText(captureSource, "LABBIT_CAPTURE_EDITABLE === '1'", 'capture-ui.mjs')
  requireText(captureSource, 'if (captureEditable) await collectEditableSnapshot', 'capture-ui.mjs')
  requireText(bundleSource, "LABBIT_CAPTURE_EDITABLE: '1'", 'prepare-figma-bundle.mjs')
  requireText(bundleSource, '14-lab-spec-forbidden.png', 'prepare-figma-bundle.mjs')
  forbidText(bundleSource, '14-lab-spec-readonly.png', 'prepare-figma-bundle.mjs')
  requireText(bundleSource, 'Editable DOM 화면 목록이 Figma manifest와 일치하지 않습니다.', 'prepare-figma-bundle.mjs')

  requireText(code, 'figma.showUI(__html__', 'code.js')
  requireText(code, 'await figma.setCurrentPageAsync(page)', 'code.js')
  requireText(code, "type: 'IMPORT_DONE'", 'code.js')
  requireText(code, "type: 'IMPORT_ERROR'", 'code.js')
  requireText(code, "message?.type !== 'IMPORT_BUNDLE'", 'code.js')
  requireText(code, "Editable · DOM", 'code.js')
  requireText(code, 'editableData', 'code.js')
  requireText(code, 'setPrototypeNavigation', 'code.js')
  requireText(code, 'Prototype 연결 요약', 'code.js')
  requireText(code, 'setReactionsAsync', 'code.js')
  requireText(code, 'Component 후보 자동 분류', 'code.js')
  forbidText(code, 'figma.currentPage =', 'code.js')

  requireText(ui, "type: 'IMPORT_BUNDLE'", 'ui.html')
  requireText(ui, "message.type === 'IMPORT_DONE'", 'ui.html')
  requireText(ui, "message.type === 'IMPORT_ERROR'", 'ui.html')
  requireText(ui, 'manifest.json', 'ui.html')
  requireText(ui, 'editable-dom.json', 'ui.html')
  requireText(ui, 'editableData', 'ui.html')

  for (const script of [
    path.join('scripts', 'capture-ui.mjs'),
    path.join('scripts', 'prepare-figma-bundle.mjs'),
  ]) {
    const result = spawnSync(process.execPath, ['--check', script], {
      cwd: process.cwd(),
      encoding: 'utf8',
    })
    if (result.status !== 0) {
      throw new Error(`${script}: 문법 검사 실패\n${result.stderr || result.stdout}`)
    }
  }

  new Function(code)
  const uiScript = ui.match(/<script>([\s\S]*?)<\/script>/)?.[1]
  if (!uiScript) throw new Error('ui.html: inline script가 없습니다.')
  new Function(uiScript)

  console.log('✓ Labbit UX Importer 정적 검증 PASS')
  console.log('- manifest 형식')
  console.log('- dynamic-page 페이지 전환')
  console.log('- UI ↔ plugin 메시지 계약')
  console.log('- editable DOM import 경계')
  console.log('- Prototype 연결 경계')
  console.log('- Component 후보 자동 분류')
  console.log('- owner-only LabSpec과 opt-in Editable DOM 경계')
  console.log('- capture/bundle/plugin JavaScript 문법')
}

main().catch((error) => {
  console.error(`\nLabbit UX Importer 검증 실패\n${error instanceof Error ? error.message : error}`)
  process.exitCode = 1
})
