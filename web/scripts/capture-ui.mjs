import { spawn } from 'node:child_process'
import { existsSync } from 'node:fs'
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'

const baseUrl = (process.env.LABBIT_CAPTURE_BASE_URL ?? 'http://127.0.0.1:5173').replace(/\/$/, '')
const outputDir = path.resolve(process.env.LABBIT_CAPTURE_DIR ?? 'ui-captures')
const port = Number(process.env.LABBIT_CAPTURE_DEBUG_PORT ?? '9333')

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

function chromeCandidates() {
  const candidates = []
  if (process.env.CHROME_PATH) candidates.push(process.env.CHROME_PATH)

  if (process.platform === 'win32') {
    for (const root of [
      process.env.LOCALAPPDATA,
      process.env.PROGRAMFILES,
      process.env['PROGRAMFILES(X86)'],
    ]) {
      if (!root) continue
      candidates.push(path.join(root, 'Google', 'Chrome', 'Application', 'chrome.exe'))
      candidates.push(path.join(root, 'Microsoft', 'Edge', 'Application', 'msedge.exe'))
    }
  } else if (process.platform === 'darwin') {
    candidates.push('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
    candidates.push('/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge')
  } else {
    candidates.push('/usr/bin/google-chrome')
    candidates.push('/usr/bin/google-chrome-stable')
    candidates.push('/usr/bin/chromium')
    candidates.push('/usr/bin/chromium-browser')
    candidates.push('/usr/bin/microsoft-edge')
  }

  return candidates
}

function findChrome() {
  return chromeCandidates().find((candidate) => candidate && existsSync(candidate))
}

async function waitForHttp(url, timeoutMs = 10000) {
  const started = Date.now()
  let lastError
  while (Date.now() - started < timeoutMs) {
    try {
      const response = await fetch(url)
      if (response.ok) return response
      lastError = new Error(`${response.status} ${response.statusText}`)
    } catch (error) {
      lastError = error
    }
    await delay(150)
  }
  throw new Error(`시간 내 응답을 받지 못했습니다: ${url}\n${lastError ?? ''}`)
}

class CdpClient {
  constructor(socket) {
    this.socket = socket
    this.nextId = 1
    this.pending = new Map()
    this.events = new Map()

    socket.addEventListener('message', (event) => {
      const message = JSON.parse(event.data)
      if (message.id) {
        const pending = this.pending.get(message.id)
        if (!pending) return
        this.pending.delete(message.id)
        if (message.error) pending.reject(new Error(message.error.message))
        else pending.resolve(message.result ?? {})
        return
      }

      const listeners = this.events.get(message.method) ?? []
      for (const listener of listeners) listener(message.params ?? {})
    })
  }

  send(method, params = {}) {
    const id = this.nextId++
    this.socket.send(JSON.stringify({ id, method, params }))
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject })
    })
  }

  once(method, timeoutMs = 10000) {
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        const listeners = this.events.get(method) ?? []
        this.events.set(method, listeners.filter((listener) => listener !== onEvent))
        reject(new Error(`CDP 이벤트 대기 시간 초과: ${method}`))
      }, timeoutMs)

      const onEvent = (params) => {
        clearTimeout(timer)
        const listeners = this.events.get(method) ?? []
        this.events.set(method, listeners.filter((listener) => listener !== onEvent))
        resolve(params)
      }

      const listeners = this.events.get(method) ?? []
      this.events.set(method, [...listeners, onEvent])
    })
  }

  close() {
    this.socket.close()
  }
}

async function connectCdp(webSocketDebuggerUrl) {
  const socket = new WebSocket(webSocketDebuggerUrl)
  await new Promise((resolve, reject) => {
    socket.addEventListener('open', resolve, { once: true })
    socket.addEventListener('error', reject, { once: true })
  })
  return new CdpClient(socket)
}

async function evaluate(cdp, expression) {
  const result = await cdp.send('Runtime.evaluate', {
    expression,
    awaitPromise: true,
    returnByValue: true,
  })
  if (result.exceptionDetails) {
    throw new Error(result.exceptionDetails.text ?? '브라우저 스크립트 실행 실패')
  }
  return result.result?.value
}

async function waitForJs(cdp, expression, label, timeoutMs = 8000) {
  const started = Date.now()
  while (Date.now() - started < timeoutMs) {
    if (await evaluate(cdp, `Boolean(${expression})`)) return
    await delay(100)
  }
  throw new Error(`화면 준비 대기 시간 초과: ${label}`)
}

async function hardNavigate(cdp, url) {
  const loaded = cdp.once('Page.loadEventFired')
  await cdp.send('Page.navigate', { url })
  await loaded
  await waitForJs(cdp, 'document.readyState === "complete"', url)
}

async function spaNavigate(cdp, pathname, readyExpression, label) {
  await evaluate(
    cdp,
    `(() => {
      const nextState = { ...(history.state ?? {}), idx: ((history.state && history.state.idx) ?? 0) + 1 };
      history.pushState(nextState, '', ${JSON.stringify(pathname)});
      window.dispatchEvent(new PopStateEvent('popstate', { state: nextState }));
      return location.pathname;
    })()`,
  )
  await waitForJs(cdp, `location.pathname === ${JSON.stringify(pathname)} && (${readyExpression})`, label)
  await delay(180)
}

async function capture(cdp, filename) {
  await evaluate(
    cdp,
    'new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)))',
  )
  const metrics = await cdp.send('Page.getLayoutMetrics')
  const width = Math.max(1280, Math.ceil(metrics.cssContentSize?.width ?? 1600))
  const height = Math.max(900, Math.ceil(metrics.cssContentSize?.height ?? 1000))
  const result = await cdp.send('Page.captureScreenshot', {
    format: 'png',
    captureBeyondViewport: true,
    fromSurface: true,
    clip: { x: 0, y: 0, width, height, scale: 1 },
  })
  const destination = path.join(outputDir, filename)
  await writeFile(destination, Buffer.from(result.data, 'base64'))
  console.log(`✓ ${filename}`)
}

async function login(cdp) {
  await waitForJs(cdp, 'document.querySelector("form.login-form")', '로그인 폼')
  await evaluate(
    cdp,
    `(() => {
      const setValue = (name, value) => {
        const input = document.querySelector('input[name="' + name + '"]');
        if (!input) throw new Error(name + ' input not found');
        const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set;
        setter.call(input, value);
        input.dispatchEvent(new Event('input', { bubbles: true }));
        input.dispatchEvent(new Event('change', { bubbles: true }));
      };
      setValue('username', 'heechul');
      setValue('password', 'password');
      document.querySelector('form.login-form').requestSubmit();
    })()`,
  )
  await waitForJs(
    cdp,
    'location.pathname === "/classes" && document.querySelector(".class-grid")',
    'Mock 로그인 완료',
  )
  await delay(180)
}

async function ensureVite() {
  try {
    await waitForHttp(baseUrl, 700)
    console.log('✓ 기존 Vite 서버 사용')
    return null
  } catch {
    // 캡처 명령 하나만으로 실행할 수 있도록 Local Vite를 자동 기동합니다.
  }

  const url = new URL(baseUrl)
  if (!['127.0.0.1', 'localhost'].includes(url.hostname)) {
    throw new Error(
      `대상 서버에 연결할 수 없습니다: ${baseUrl}\nLocal 주소가 아니어서 Vite를 자동 실행하지 않았습니다.`,
    )
  }

  const viteBin = path.resolve('node_modules', 'vite', 'bin', 'vite.js')
  if (!existsSync(viteBin)) {
    throw new Error(
      'Vite 실행 파일을 찾지 못했습니다. web 폴더에서 npm.cmd install 또는 npm.cmd ci를 먼저 실행해 주세요.',
    )
  }

  const host = '127.0.0.1'
  const vitePort = url.port || '5173'
  console.log(`- Vite 자동 실행: http://${host}:${vitePort}`)

  const vite = spawn(
    process.execPath,
    [viteBin, '--host', host, '--port', vitePort, '--strictPort'],
    {
      cwd: process.cwd(),
      stdio: 'ignore',
      windowsHide: true,
    },
  )

  let exited = false
  vite.once('exit', () => {
    exited = true
  })

  try {
    await waitForHttp(baseUrl, 15000)
    if (exited) {
      throw new Error('Vite 프로세스가 시작 직후 종료되었습니다.')
    }
    console.log('✓ Vite 준비 완료')
    return vite
  } catch (error) {
    vite.kill()
    throw new Error(
      `Vite 자동 실행에 실패했습니다.\n${error instanceof Error ? error.message : error}`,
    )
  }
}

async function main() {
  console.log('Labbit UI capture 시작')
  console.log(`- 대상: ${baseUrl}`)
  console.log(`- 저장: ${outputDir}`)

  const vite = await ensureVite()

  const chrome = findChrome()
  if (!chrome) {
    throw new Error('Chrome/Edge 실행 파일을 찾지 못했습니다. CHROME_PATH 환경변수로 경로를 지정해 주세요.')
  }

  await mkdir(outputDir, { recursive: true })
  const profileDir = await mkdtemp(path.join(os.tmpdir(), 'labbit-ui-capture-'))

  const browser = spawn(
    chrome,
    [
      '--headless=new',
      `--remote-debugging-port=${port}`,
      `--user-data-dir=${profileDir}`,
      '--no-first-run',
      '--no-default-browser-check',
      '--disable-background-networking',
      '--disable-sync',
      '--hide-scrollbars',
      '--window-size=1600,1000',
      'about:blank',
    ],
    { stdio: 'ignore' },
  )

  let cdp
  try {
    await waitForHttp(`http://127.0.0.1:${port}/json/version`, 10000)
    const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()
    const page = targets.find((target) => target.type === 'page')
    if (!page?.webSocketDebuggerUrl) {
      throw new Error('Chrome DevTools page target을 찾지 못했습니다.')
    }

    cdp = await connectCdp(page.webSocketDebuggerUrl)
    await cdp.send('Page.enable')
    await cdp.send('Runtime.enable')
    await cdp.send('Emulation.setDeviceMetricsOverride', {
      width: 1600,
      height: 1000,
      deviceScaleFactor: 1,
      mobile: false,
    })

    await hardNavigate(cdp, `${baseUrl}/login`)
    await capture(cdp, '01-login.png')

    await login(cdp)
    await capture(cdp, '02-class-list.png')

    const screens = [
      {
        path: '/classes/class-kubernetes-basic',
        ready:
          'document.querySelector("h1")?.textContent?.includes("Kubernetes Basic") && document.querySelector(".class-overview-card")',
        label: 'Kubernetes 상세',
        file: '03-class-active.png',
      },
      {
        path: '/classes/class-kubernetes-basic/lab',
        ready: 'document.querySelector("[aria-label=\"Lab Workspace Shell\"]")',
        label: 'Lab Workspace',
        file: '04-lab-workspace.png',
      },
      {
        path: '/classes/class-docker-basic',
        ready:
          'document.querySelector("h1")?.textContent?.includes("Docker Basic") && document.querySelector(".class-overview-card")',
        label: 'Docker 상세',
        file: '05-class-empty-instructor.png',
      },
      {
        path: '/classes/class-linux-networking',
        ready:
          'document.querySelector("h1")?.textContent?.includes("Linux Networking") && document.querySelector(".class-overview-card")',
        label: '학생 Class 상세',
        file: '06-class-empty-student.png',
      },
      {
        path: '/classes/class-docker-basic/provision',
        ready: 'document.querySelector("h1")?.textContent?.includes("새 환경 생성")',
        label: 'Provision',
        file: '07-provision-select.png',
      },
      {
        path: '/lab-executions/execution-kubernetes-basic',
        ready: 'document.querySelector("h1")?.textContent?.includes("실습 운영 상태")',
        label: 'LabExecution',
        file: '08-lab-execution.png',
      },
      {
        path: '/lab-specs',
        ready: 'document.querySelector("main") && document.body.textContent.includes("LabSpec")',
        label: 'LabSpec 목록',
        file: '09-lab-specs.png',
      },
    ]

    for (const screen of screens) {
      await spaNavigate(cdp, screen.path, screen.ready, screen.label)
      await capture(cdp, screen.file)
    }

    console.log(`\n완료: ${outputDir}`)
  } finally {
    cdp?.close()
    browser.kill()
    vite?.kill()
    await rm(profileDir, { recursive: true, force: true }).catch(() => {})
  }
}

main().catch((error) => {
  console.error(`\nUI capture 실패\n${error instanceof Error ? error.message : error}`)
  process.exitCode = 1
})
