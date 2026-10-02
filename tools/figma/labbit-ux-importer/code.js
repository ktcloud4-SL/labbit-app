figma.showUI(__html__, { width: 460, height: 620, themeColors: true })

const CARD_WIDTH = 1240
const IMAGE_MAX_WIDTH = 1200
const COLUMN_GAP = 80
const ROW_GAP = 100
const GROUP_GAP = 180

function rgb(hex) {
  const value = hex.replace('#', '')
  return {
    r: parseInt(value.slice(0, 2), 16) / 255,
    g: parseInt(value.slice(2, 4), 16) / 255,
    b: parseInt(value.slice(4, 6), 16) / 255,
  }
}

async function loadFonts() {
  await figma.loadFontAsync({ family: 'Inter', style: 'Regular' })
  await figma.loadFontAsync({ family: 'Inter', style: 'Medium' })
}

function textNode(characters, size = 18, medium = false) {
  const node = figma.createText()
  node.fontName = { family: 'Inter', style: medium ? 'Medium' : 'Regular' }
  node.fontSize = size
  node.characters = characters
  node.fills = [{ type: 'SOLID', color: rgb('#1F2937') }]
  return node
}

function createCardBase(name) {
  const card = figma.createFrame()
  card.name = name
  card.layoutMode = 'VERTICAL'
  card.primaryAxisSizingMode = 'AUTO'
  card.counterAxisSizingMode = 'FIXED'
  card.resize(CARD_WIDTH, 100)
  card.paddingTop = 20
  card.paddingRight = 20
  card.paddingBottom = 20
  card.paddingLeft = 20
  card.itemSpacing = 12
  card.cornerRadius = 12
  card.fills = [{ type: 'SOLID', color: rgb('#FFFFFF') }]
  card.strokes = [{ type: 'SOLID', color: rgb('#D1D5DB') }]
  card.strokeWeight = 1
  return card
}

async function createScreenCard(screen, fileBytes) {
  const card = createCardBase(screen.title)
  card.appendChild(textNode(screen.title, 24, true))

  const meta = [
    screen.route ? `Route: ${screen.route}` : null,
    screen.jira?.length ? `Jira: ${screen.jira.join(', ')}` : null,
    screen.status ? `상태: ${screen.status}` : null,
  ].filter(Boolean).join('  ·  ')

  const metaNode = textNode(meta, 14)
  metaNode.fills = [{ type: 'SOLID', color: rgb('#6B7280') }]
  card.appendChild(metaNode)

  const image = figma.createImage(fileBytes)
  const size = await image.getSizeAsync()
  const scale = Math.min(1, IMAGE_MAX_WIDTH / size.width)
  const width = Math.max(1, Math.round(size.width * scale))
  const height = Math.max(1, Math.round(size.height * scale))

  const rect = figma.createRectangle()
  rect.name = screen.file
  rect.resize(width, height)
  rect.fills = [{
    type: 'IMAGE',
    imageHash: image.hash,
    scaleMode: 'FIT',
  }]
  rect.cornerRadius = 8
  card.appendChild(rect)

  return card
}

function createPlannedCard(item) {
  const card = createCardBase(`계획 · ${item.title}`)
  card.resize(CARD_WIDTH, 100)
  card.fills = [{ type: 'SOLID', color: rgb('#FFFBEB') }]
  card.strokes = [{ type: 'SOLID', color: rgb('#F59E0B') }]

  const title = textNode(`계획 상태 · ${item.title}`, 22, true)
  card.appendChild(title)

  if (item.jira?.length) {
    const jira = textNode(`Jira: ${item.jira.join(', ')}`, 14)
    jira.fills = [{ type: 'SOLID', color: rgb('#92400E') }]
    card.appendChild(jira)
  }

  const note = textNode(item.note ?? '', 16)
  note.textAutoResize = 'HEIGHT'
  note.resize(CARD_WIDTH - 40, note.height)
  card.appendChild(note)

  return card
}

function createGroupTitle(title) {
  const node = textNode(title, 34, true)
  node.name = title
  return node
}

async function importBundle(manifest, files) {
  if (!manifest || manifest.schemaVersion !== 1) {
    throw new Error('지원하지 않는 manifest 형식입니다.')
  }

  const fileMap = new Map(files.map((file) => [file.name, file.bytes]))
  const page = figma.createPage()
  page.name = manifest.pageName || 'Labbit UX Review'
  figma.currentPage = page

  const title = textNode(manifest.pageName || 'Labbit UX Review', 44, true)
  title.x = 0
  title.y = 0
  page.appendChild(title)

  const subtitle = textNode(
    `자동 캡처 기반 UX 검토 보드 · ${manifest.captureMode ?? 'unknown'} · ${manifest.generatedAt ?? ''}`,
    16,
  )
  subtitle.fills = [{ type: 'SOLID', color: rgb('#6B7280') }]
  subtitle.x = 0
  subtitle.y = 64
  page.appendChild(subtitle)

  let groupY = 140

  for (const group of manifest.groups ?? []) {
    const groupTitle = createGroupTitle(group.title)
    groupTitle.x = 0
    groupTitle.y = groupY
    page.appendChild(groupTitle)

    let x = 0
    let y = groupY + 60
    let rowHeight = 0
    let index = 0

    for (const screen of group.screens ?? []) {
      const bytes = fileMap.get(screen.file)
      if (!bytes) {
        throw new Error(`스크린샷 파일이 없습니다: ${screen.file}`)
      }

      const card = await createScreenCard(screen, bytes)
      card.x = x
      card.y = y
      page.appendChild(card)

      rowHeight = Math.max(rowHeight, card.height)
      index += 1

      if (index % 2 === 0) {
        x = 0
        y += rowHeight + ROW_GAP
        rowHeight = 0
      } else {
        x = CARD_WIDTH + COLUMN_GAP
      }
    }

    if (index % 2 !== 0) {
      y += rowHeight + ROW_GAP
    }

    const planned = (manifest.plannedStates ?? []).filter(
      (item) => item.groupId === group.id,
    )

    for (const item of planned) {
      const card = createPlannedCard(item)
      card.x = 0
      card.y = y
      page.appendChild(card)
      y += card.height + 28
    }

    groupY = y + GROUP_GAP
  }

  const allNodes = page.children
  if (allNodes.length) {
    figma.viewport.scrollAndZoomIntoView(allNodes)
  }

  figma.ui.postMessage({
    type: 'IMPORT_DONE',
    pageName: page.name,
    screenCount: (manifest.groups ?? []).reduce(
      (sum, group) => sum + (group.screens?.length ?? 0),
      0,
    ),
    plannedCount: manifest.plannedStates?.length ?? 0,
  })
}

figma.ui.onmessage = async (message) => {
  if (message?.type === 'CLOSE') {
    figma.closePlugin()
    return
  }

  if (message?.type !== 'IMPORT_BUNDLE') return

  try {
    await loadFonts()
    await importBundle(message.manifest, message.files ?? [])
  } catch (error) {
    const detail = error instanceof Error ? error.message : String(error)
    figma.ui.postMessage({ type: 'IMPORT_ERROR', message: detail })
  }
}
