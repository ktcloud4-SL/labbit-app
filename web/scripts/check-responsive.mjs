/** LBT-53: disposable localhost Mock browser layout smoke (not live E2E). */
import { spawn } from 'node:child_process'
import { existsSync } from 'node:fs'
import { mkdtemp, rm } from 'node:fs/promises'
import { createServer } from 'node:net'
import os from 'node:os'
import path from 'node:path'

const widths = [320, 375, 768]
const screens = [
  ['/classes', '.class-grid', 'class-list'],
  ['/classes/class-kubernetes-basic', '.class-overview-card', 'class-detail'],
  ['/lab-specs', 'main', 'labspec-list'],
  ['/lab-specs/new', 'form.labspec-form', 'labspec-new-form'],
]
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

function findChrome() {
  const paths = [process.env.CHROME_PATH]
  if (process.platform === 'win32') {
    for (const root of [process.env.LOCALAPPDATA, process.env.PROGRAMFILES, process.env['PROGRAMFILES(X86)']]) {
      if (!root) continue
      paths.push(path.join(root, 'Google', 'Chrome', 'Application', 'chrome.exe'))
      paths.push(path.join(root, 'Microsoft', 'Edge', 'Application', 'msedge.exe'))
    }
  } else if (process.platform === 'darwin') {
    paths.push('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
  } else {
    paths.push('/usr/bin/google-chrome', '/usr/bin/google-chrome-stable')
    paths.push('/usr/bin/chromium', '/usr/bin/chromium-browser')
  }
  return paths.find((p) => p && existsSync(p))
}

async function availablePort() {
  const server = createServer()
  return new Promise((resolve, reject) => {
    server.once('error', reject)
    server.listen(0, '127.0.0.1', () => {
      const value = server.address().port
      server.close(() => resolve(value))
    })
  })
}

async function waitForUrl(url, label) {
  for (let i = 0; i < 120; i++) {
    try {
      const response = await fetch(url)
      if (response.ok) return response
    } catch { /* startup in progress */ }
    await sleep(125)
  }
  throw new Error('isolated localhost service did not start: ' + label)
}

class Cdp {
  constructor(socket) {
    this.socket = socket
    this.nextId = 1
    this.pending = new Map()
    socket.addEventListener('message', ({ data }) => {
      const frame = JSON.parse(data)
      const task = this.pending.get(frame.id)
      if (!task) return
      this.pending.delete(frame.id)
      clearTimeout(task.timer)
      if (frame.error) task.reject(new Error(frame.error.message))
      else task.resolve(frame.result ?? {})
    })
  }

  send(method, params = {}) {
    const id = this.nextId++
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id)
        reject(new Error('CDP command timed out: ' + method))
      }, 12000)
      this.pending.set(id, { resolve, reject, timer })
      this.socket.send(JSON.stringify({ id, method, params }))
    })
  }

  async evaluate(expression) {
    const response = await this.send('Runtime.evaluate', {
      expression, returnByValue: true, awaitPromise: true,
    })
    if (response.exceptionDetails) throw new Error('browser evaluation failed')
    return response.result?.value
  }

  close() { this.socket.close() }
}

async function connectChrome(port) {
  const targets = await (await fetch('http://127.0.0.1:' + port + '/json/list')).json()
  const url = targets.find((t) => t.type === 'page')?.webSocketDebuggerUrl
  if (!url) throw new Error('isolated Chrome page was not found')
  const ws = new WebSocket(url)
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve, { once: true })
    ws.addEventListener('error', reject, { once: true })
  })
  return new Cdp(ws)
}

async function mockLogin(cdp) {
  // Only the isolated local Mock fixture account; no real credentials or cookies.
  await cdp.evaluate([
    '(() => {',
    'const set = (name, value) => {',
    'const el = document.querySelector(\'input[name="\' + name + \'"]\');',
    'const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set;',
    'setter.call(el, value);',
    'el.dispatchEvent(new Event("input", { bubbles: true }));',
    'el.dispatchEvent(new Event("change", { bubbles: true }));',
    '};',
    'set("username", "heechul");',
    'set("password", "password");',
    'document.querySelector("form.login-form").requestSubmit();',
    '})()',
  ].join(''))
}

async function testScreen(cdp, base, screen, viewport) {
  const [screenPath, selector, label] = screen
  await cdp.send('Page.navigate', { url: base + screenPath })
  let loggedIn = false
  let ready = false
  for (let attempt = 0; attempt < 100; attempt++) {
    let state
    try {
      state = await cdp.evaluate('({ path: location.pathname, login: Boolean(document.querySelector("form.login-form")), ready: location.pathname === ' +
        JSON.stringify(screenPath) + ' && Boolean(document.querySelector(' + JSON.stringify(selector) + ')) })')
    } catch {
      await sleep(120) // navigation replaced the JavaScript execution context
      continue
    }
    if (state.ready) { ready = true; break }
    if (state.path === '/login' && state.login && !loggedIn) {
      await mockLogin(cdp)
      loggedIn = true
    }
    await sleep(120)
  }
  if (!ready) throw new Error(label + ' did not render in disposable Mock mode')
  await sleep(180)
  const dimensions = await cdp.evaluate('({ width: innerWidth, client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth })')
  const overflow = dimensions.scroll - dimensions.client
  if (dimensions.width !== viewport || overflow > 2 || overflow < 0) {
    throw new Error(label + ' @' + viewport + 'px: actual width ' + dimensions.width + ', overflow ' + overflow + 'px')
  }
  console.log('PASS ' + label + ' @' + viewport + 'px root overflow ' + overflow + 'px')
}

async function main() {
  const chrome = findChrome()
  if (!chrome) throw new Error('Chrome/Chromium unavailable; set CHROME_PATH')
  const vitePort = await availablePort()
  let chromePort = await availablePort()
  while (chromePort === vitePort) chromePort = await availablePort()
  const base = 'http://127.0.0.1:' + vitePort
  const profile = await mkdtemp(path.join(os.tmpdir(), 'labbit-mobile-mock-'))
  let vite, browser, cdp
  try {
    const viteBin = path.resolve('node_modules/vite/bin/vite.js')
    if (!existsSync(viteBin)) throw new Error('Run npm ci in web before QA')
    vite = spawn(process.execPath, [viteBin, '--host', '127.0.0.1', '--port', String(vitePort), '--strictPort'], {
      cwd: process.cwd(), env: { ...process.env, VITE_LABBIT_API_MODE: 'mock' }, stdio: 'ignore',
    })
    await waitForUrl(base, 'Mock Vite')
    const args = [
      '--headless=new', '--no-proxy-server', '--no-first-run', '--no-default-browser-check',
      '--disable-sync', '--disable-background-networking', '--window-size=1000,900',
      '--remote-debugging-port=' + chromePort, '--user-data-dir=' + profile, 'about:blank',
    ]
    if (typeof process.getuid === 'function' && process.getuid() === 0) args.push('--no-sandbox')
    browser = spawn(chrome, args, { stdio: 'ignore' })
    await waitForUrl('http://127.0.0.1:' + chromePort + '/json/version', 'Chrome CDP')
    cdp = await connectChrome(chromePort)
    await cdp.send('Page.enable')
    await cdp.send('Runtime.enable')
    for (const width of widths) {
      await cdp.send('Emulation.setDeviceMetricsOverride', {
        width, height: 900, deviceScaleFactor: 1, mobile: true,
      })
      for (const screen of screens) await testScreen(cdp, base, screen, width)
    }
    console.log('PASS: LBT-53 Mock responsive smoke; real Local/AWS E2E not covered')
  } finally {
    cdp?.close()
    browser?.kill()
    vite?.kill()
    await rm(profile, { recursive: true, force: true }).catch(() => {})
  }
}

main().catch((error) => {
  console.error('Responsive Mock QA failed: ' + error.message)
  process.exitCode = 1
})
