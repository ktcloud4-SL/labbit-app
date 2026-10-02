figma.showUI(__html__, { width: 480, height: 660, themeColors: true })

const CARD_WIDTH = 1240
const PANEL_WIDTH = 580
const COLUMN_GAP = 24
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

function parseCssColor(value) {
  if (!value || value === 'transparent' || value === 'rgba(0, 0, 0, 0)') return null

  const rgba = value.match(
    /^rgba?\(\s*(\d+(?:\.\d+)?)\s*,\s*(\d+(?:\.\d+)?)\s*,\s*(\d+(?:\.\d+)?)(?:\s*,\s*(\d+(?:\.\d+)?))?\s*\)$/i,
  )
  if (rgba) {
    return {
      color: {
        r: Math.max(0, Math.min(255, Number(rgba[1]))) / 255,
        g: Math.max(0, Math.min(255, Number(rgba[2]))) / 255,
        b: Math.max(0, Math.min(255, Number(rgba[3]))) / 255,
      },
      opacity: rgba[4] == null ? 1 : Math.max(0, Math.min(1, Number(rgba[4]))),
    }
  }

  if (/^#[0-9a-f]{6}$/i.test(value)) {
    return { color: rgb(value), opacity: 1 }
  }

  return null
}

function solidPaint(value) {
  const parsed = parseCssColor(value)
  if (!parsed) return null
  return {
    type: 'SOLID',
    color: parsed.color,
    opacity: parsed.opacity,
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

function applyTextColor(node, value) {
  const paint = solidPaint(value)
  if (paint) node.fills = [paint]
}

function createCardBase(name) {
  const card = figma.createFrame()
  card.name = name
  card.layoutMode = 'VERTICAL'
  card.resize(CARD_WIDTH, 100)
  card.primaryAxisSizingMode = 'AUTO'
  card.counterAxisSizingMode = 'FIXED'
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

function createReferenceFrame(fileBytes) {
  const holder = figma.createFrame()
  holder.name = 'Reference · Screenshot'
  holder.layoutMode = 'VERTICAL'
  holder.primaryAxisSizingMode = 'AUTO'
  holder.counterAxisSizingMode = 'FIXED'
  holder.resize(PANEL_WIDTH, 100)
  holder.itemSpacing = 8
  holder.fills = []

  const label = textNode('Reference · Screenshot', 13, true)
  label.fills = [{ type: 'SOLID', color: rgb('#6B7280') }]
  holder.appendChild(label)

  return { holder, attach: async () => {
    const image = figma.createImage(fileBytes)
    const size = await image.getSizeAsync()
    const scale = PANEL_WIDTH / Math.max(1, size.width)
    const rect = figma.createRectangle()
    rect.name = 'Reference image'
    rect.resize(PANEL_WIDTH, Math.max(1, Math.round(size.height * scale)))
    rect.fills = [{
      type: 'IMAGE',
      imageHash: image.hash,
      scaleMode: 'FIT',
    }]
    rect.cornerRadius = 8
    holder.appendChild(rect)
  }}
}

function createContainerNode(element, scale) {
  const rect = figma.createRectangle()
  rect.name = element.className
    ? `Container · ${element.className.split(' ').slice(0, 2).join('.')}`
    : 'Container'
  rect.x = element.rect.x * scale
  rect.y = element.rect.y * scale
  rect.resize(
    Math.max(1, element.rect.width * scale),
    Math.max(1, element.rect.height * scale),
  )

  const fill = solidPaint(element.style.backgroundColor)
  rect.fills = fill ? [fill] : []

  const stroke = solidPaint(element.style.borderColor)
  if (stroke && element.style.borderWidth > 0) {
    rect.strokes = [stroke]
    rect.strokeWeight = Math.max(0.5, element.style.borderWidth * scale)
  } else {
    rect.strokes = []
  }

  rect.cornerRadius = Math.max(0, Math.min(100, element.style.borderRadius * scale))
  rect.opacity = Math.max(0, Math.min(1, element.style.opacity ?? 1))
  return rect
}

function createEditableText(element, scale) {
  const value = String(element.text ?? '').trim()
  if (!value) return null

  const fontSize = Math.max(6, Math.min(48, (element.style.fontSize || 14) * scale))
  const medium = Number(element.style.fontWeight || 400) >= 600
  const node = textNode(value, fontSize, medium)
  node.name = `Text · ${value.slice(0, 40)}`
  applyTextColor(node, element.style.color)
  node.x = element.rect.x * scale
  node.y = element.rect.y * scale
  node.resize(
    Math.max(20, element.rect.width * scale),
    Math.max(fontSize + 2, element.rect.height * scale),
  )
  node.textAutoResize = 'HEIGHT'

  if (element.style.textAlign === 'center') node.textAlignHorizontal = 'CENTER'
  if (element.style.textAlign === 'right') node.textAlignHorizontal = 'RIGHT'
  node.opacity = Math.max(0, Math.min(1, element.style.opacity ?? 1))
  return node
}

function createControlNodes(element, scale) {
  const nodes = []
  const x = element.rect.x * scale
  const y = element.rect.y * scale
  const width = Math.max(2, element.rect.width * scale)
  const height = Math.max(2, element.rect.height * scale)
  const type = element.controlType

  if (type === 'checkbox' || type === 'radio') {
    const shape = type === 'radio' ? figma.createEllipse() : figma.createRectangle()
    shape.name = `Control · ${type}`
    shape.x = x
    shape.y = y
    shape.resize(width, height)
    const fill = solidPaint(element.style.backgroundColor)
    shape.fills = fill ? [fill] : [{ type: 'SOLID', color: rgb('#FFFFFF') }]
    const stroke = solidPaint(element.style.borderColor)
    shape.strokes = stroke ? [stroke] : [{ type: 'SOLID', color: rgb('#CBD5E1') }]
    shape.strokeWeight = Math.max(0.5, (element.style.borderWidth || 1) * scale)
    if (type === 'checkbox') shape.cornerRadius = Math.max(1, element.style.borderRadius * scale)
    nodes.push(shape)

    if (element.text) {
      const mark = textNode(element.text, Math.max(7, height * 0.7), true)
      mark.name = 'Control value'
      mark.x = x + width * 0.12
      mark.y = y + height * 0.02
      mark.resize(Math.max(10, width * 0.76), Math.max(10, height * 0.8))
      mark.textAlignHorizontal = 'CENTER'
      nodes.push(mark)
    }
    return nodes
  }

  const hasBox =
    Boolean(solidPaint(element.style.backgroundColor)) ||
    element.style.borderWidth > 0 ||
    ['button', 'input', 'textarea', 'select'].includes(type)

  if (hasBox) {
    const rect = figma.createRectangle()
    rect.name = `Control · ${type}`
    rect.x = x
    rect.y = y
    rect.resize(width, height)
    const fill = solidPaint(element.style.backgroundColor)
    rect.fills = fill ? [fill] : [{ type: 'SOLID', color: rgb('#FFFFFF') }]
    const stroke = solidPaint(element.style.borderColor)
    rect.strokes = stroke ? [stroke] : []
    rect.strokeWeight = Math.max(0.5, (element.style.borderWidth || 0) * scale)
    rect.cornerRadius = Math.max(0, Math.min(100, element.style.borderRadius * scale))
    rect.opacity = Math.max(0, Math.min(1, element.style.opacity ?? 1))
    nodes.push(rect)
  }

  const text = String(element.text ?? '').trim()
  if (text) {
    const fontSize = Math.max(6, Math.min(40, (element.style.fontSize || 14) * scale))
    const node = textNode(text, fontSize, Number(element.style.fontWeight || 400) >= 600)
    node.name = `Control text · ${text.slice(0, 40)}`
    applyTextColor(node, element.style.color)
    node.x = x + (hasBox ? Math.min(8, width * 0.08) : 0)
    node.y = y + Math.max(0, (height - fontSize * 1.25) / 2)
    node.resize(
      Math.max(10, width - (hasBox ? Math.min(16, width * 0.16) : 0)),
      Math.max(fontSize + 2, height),
    )
    node.textAutoResize = 'HEIGHT'
    if (element.style.textAlign === 'center' || type === 'button') {
      node.textAlignHorizontal = 'CENTER'
    } else if (element.style.textAlign === 'right') {
      node.textAlignHorizontal = 'RIGHT'
    }
    nodes.push(node)
  }

  return nodes
}

function createEditableScreen(snapshot) {
  const scale = PANEL_WIDTH / Math.max(1, snapshot.width || 1600)
  const frame = figma.createFrame()
  frame.name = 'Editable · DOM reconstruction'
  frame.layoutMode = 'NONE'
  frame.clipsContent = true
  frame.resize(
    PANEL_WIDTH,
    Math.max(120, Math.round((snapshot.height || 900) * scale)),
  )

  const bg = solidPaint(snapshot.backgroundColor)
  frame.fills = bg ? [bg] : [{ type: 'SOLID', color: rgb('#F5F6FA') }]
  frame.strokes = [{ type: 'SOLID', color: rgb('#D1D5DB') }]
  frame.strokeWeight = 1
  frame.cornerRadius = 8

  const elements = Array.isArray(snapshot.elements) ? snapshot.elements : []
  const containers = elements
    .filter((element) => element.kind === 'container')
    .sort(
      (a, b) =>
        b.rect.width * b.rect.height - a.rect.width * a.rect.height ||
        a.order - b.order,
    )

  for (const element of containers) {
    frame.appendChild(createContainerNode(element, scale))
  }

  for (const element of elements.filter((element) => element.kind === 'control')) {
    for (const node of createControlNodes(element, scale)) {
      frame.appendChild(node)
    }
  }

  for (const element of elements.filter((element) => element.kind === 'text')) {
    const node = createEditableText(element, scale)
    if (node) frame.appendChild(node)
  }

  return frame
}

function createEditableColumn(snapshot) {
  const holder = figma.createFrame()
  holder.name = 'Editable · DOM'
  holder.layoutMode = 'VERTICAL'
  holder.primaryAxisSizingMode = 'AUTO'
  holder.counterAxisSizingMode = 'FIXED'
  holder.resize(PANEL_WIDTH, 100)
  holder.itemSpacing = 8
  holder.fills = []

  const label = textNode('Editable · DOM (글자/버튼/박스 수정 가능)', 13, true)
  label.fills = [{ type: 'SOLID', color: rgb('#1F805B') }]
  holder.appendChild(label)

  if (!snapshot) {
    const missing = textNode('editable-dom.json 데이터가 없습니다. capture:figma를 다시 실행해 주세요.', 13)
    missing.fills = [{ type: 'SOLID', color: rgb('#AD3D3D') }]
    missing.resize(PANEL_WIDTH, 60)
    missing.textAutoResize = 'HEIGHT'
    holder.appendChild(missing)
    return holder
  }

  holder.appendChild(createEditableScreen(snapshot))
  return holder
}

async function createScreenCard(screen, fileBytes, snapshot) {
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

  const comparison = figma.createFrame()
  comparison.name = 'Reference ↔ Editable'
  comparison.layoutMode = 'HORIZONTAL'
  comparison.primaryAxisSizingMode = 'AUTO'
  comparison.counterAxisSizingMode = 'AUTO'
  comparison.itemSpacing = COLUMN_GAP
  comparison.fills = []

  const reference = createReferenceFrame(fileBytes)
  await reference.attach()
  comparison.appendChild(reference.holder)
  comparison.appendChild(createEditableColumn(snapshot))
  card.appendChild(comparison)

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
  note.resize(CARD_WIDTH - 40, 30)
  note.textAutoResize = 'HEIGHT'
  card.appendChild(note)

  return card
}

function createGroupTitle(title) {
  const node = textNode(title, 34, true)
  node.name = title
  return node
}

async function importBundle(manifest, files, editableData) {
  if (!manifest || ![1, 2].includes(manifest.schemaVersion)) {
    throw new Error('지원하지 않는 manifest 형식입니다.')
  }

  const fileMap = new Map(files.map((file) => [file.name, file.bytes]))
  const snapshotMap = new Map(
    (editableData?.screens ?? []).map((snapshot) => [snapshot.file, snapshot]),
  )

  const page = figma.createPage()
  page.name = manifest.pageName || 'Labbit UX Review'
  await figma.setCurrentPageAsync(page)

  const title = textNode(manifest.pageName || 'Labbit UX Review', 44, true)
  title.x = 0
  title.y = 0
  page.appendChild(title)

  const subtitle = textNode(
    `자동 캡처 + editable DOM 기반 UX 검토 보드 · ${manifest.captureMode ?? 'unknown'} · ${manifest.generatedAt ?? ''}`,
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

      const card = await createScreenCard(screen, bytes, snapshotMap.get(screen.file))
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
        x = CARD_WIDTH + 80
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
    editableCount: snapshotMap.size,
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
    await importBundle(
      message.manifest,
      message.files ?? [],
      message.editableData ?? null,
    )
  } catch (error) {
    const detail = error instanceof Error ? error.message : String(error)
    figma.ui.postMessage({ type: 'IMPORT_ERROR', message: detail })
  }
}
