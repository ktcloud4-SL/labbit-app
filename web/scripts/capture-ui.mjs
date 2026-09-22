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
    const description =
      result.exceptionDetails.exception?.description ??
      result.exceptionDetails.exception?.value ??
      result.exceptionDetails.text ??
      '브라우저 스크립트 실행 실패'
    throw new Error(String(description))
  }
  return result.result?.value
}

async function waitForJs(cdp, expression, label, timeoutMs = 8000) {
  const started = Date.now()
  const encodedExpression = JSON.stringify(expression)
  while (Date.now() - started < timeoutMs) {
    if (await evaluate(cdp, `Boolean(eval(${encodedExpression}))`)) return
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

async function screenState(cdp, screen) {
  return evaluate(
    cdp,
    `(() => {
      const pathname = ${JSON.stringify(screen.path)};
      const selector = ${JSON.stringify(screen.selector ?? null)};
      const text = ${JSON.stringify(screen.text ?? null)};
      const selectorReady = !selector || Boolean(document.querySelector(selector));
      const textReady = !text || Boolean(document.body.textContent?.includes(text));

      return {
        path: location.pathname,
        targetReady:
          location.pathname === pathname &&
          selectorReady &&
          textReady,
        loginReady:
          location.pathname === '/login' &&
          Boolean(document.querySelector('form.login-form')),
      };
    })()`,
  )
}

async function waitForScreen(cdp, screen, timeoutMs = 10000) {
  const started = Date.now()
  while (Date.now() - started < timeoutMs) {
    const state = await screenState(cdp, screen)
    if (state?.targetReady) return
    await delay(100)
  }

  const currentPath = await evaluate(cdp, 'location.pathname')
  throw new Error(
    `화면 준비 대기 시간 초과: ${screen.label} (현재 경로: ${currentPath})`,
  )
}

async function navigateAuthenticated(cdp, screen) {
  await hardNavigate(cdp, `${baseUrl}${screen.path}`)

  // Mock session은 문서를 새로 로드하면 초기화됩니다.
  // 보호 Route가 /login으로 이동한 경우 자동 로그인 후 원래 Route로 복귀합니다.
  const started = Date.now()

  while (Date.now() - started < 10000) {
    const state = await screenState(cdp, screen)

    if (state?.targetReady) {
      await delay(180)
      return
    }

    if (state?.loginReady) {
      await login(cdp, screen.path)
      await waitForScreen(cdp, screen, 10000)
      await delay(180)
      return
    }

    await delay(100)
  }

  const currentPath = await evaluate(cdp, 'location.pathname')
  throw new Error(
    `화면 이동 실패: ${screen.label} (현재 경로: ${currentPath})`,
  )
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

async function login(cdp, destinationPath = '/classes') {
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
    `location.pathname === ${JSON.stringify(destinationPath)}`,
    `Mock 로그인 완료 → ${destinationPath}`,
    10000,
  )
  await delay(180)
}


async function captureProvisionOperationFlow(cdp) {
  const provisionScreen = {
    path: '/classes/class-docker-basic/provision',
    selector: 'form.labspec-form',
    text: '새 환경 생성',
    label: 'Provision 흐름',
  }

  await navigateAuthenticated(cdp, provisionScreen)

  await evaluate(
    cdp,
    `(() => {
      const select = document.querySelector('select');
      if (!select) throw new Error('LabSpec select not found');
      const setter = Object.getOwnPropertyDescriptor(
        HTMLSelectElement.prototype,
        'value',
      ).set;
      setter.call(select, 'lab-spec-kubernetes-basic');
      select.dispatchEvent(new Event('input', { bubbles: true }));
      select.dispatchEvent(new Event('change', { bubbles: true }));

      const checkbox = document.querySelector('input[type="checkbox"]');
      if (!checkbox) throw new Error('student checkbox not found');
      if (!checkbox.checked) checkbox.click();
    })()`,
  )

  await waitForJs(
    cdp,
    `document.querySelector('select')?.value === 'lab-spec-kubernetes-basic' &&
      Boolean(document.querySelector('input[type="checkbox"]')?.checked)`,
    'Provision 입력 선택',
  )

  await evaluate(
    cdp,
    `(() => {
      const form = document.querySelector('form.labspec-form');
      if (!form) throw new Error('Provision form not found');
      form.requestSubmit();
    })()`,
  )

  await waitForJs(
    cdp,
    `Boolean(document.body.textContent?.includes('환경 생성 전 확인'))`,
    'Provision 최종 확인',
  )
  await capture(cdp, '10-provision-confirm.png')

  await evaluate(
    cdp,
    `(() => {
      const button = [...document.querySelectorAll('button')].find(
        (candidate) => candidate.textContent?.trim() === '환경 생성 시작',
      );
      if (!button) throw new Error('환경 생성 시작 button not found');
      button.click();
    })()`,
  )

  await waitForJs(
    cdp,
    `location.pathname.startsWith('/operations/') &&
      Boolean(document.querySelector('.operation-grid'))`,
    'Operation 상태 화면',
    10000,
  )
  await delay(180)
  await capture(cdp, '11-operation.png')
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

  await rm(outputDir, { recursive: true, force: true }).catch(() => {})
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

    await login(cdp, '/classes')
    await waitForJs(cdp, 'document.querySelector(".class-grid")', '수업 목록')
    await capture(cdp, '02-class-list.png')

    const screens = [
      {
        path: '/classes/class-kubernetes-basic',
        selector: '.class-overview-card',
        text: 'Kubernetes Basic',
        label: 'Kubernetes 상세',
        file: '03-class-active.png',
      },
      {
        path: '/classes/class-kubernetes-basic/lab',
        selector: '[aria-label="Lab Workspace Shell"]',
        text: 'Kubernetes Basic',
        label: 'Lab Workspace',
        file: '04-lab-workspace.png',
      },
      {
        path: '/classes/class-docker-basic',
        selector: '.class-overview-card',
        text: 'Docker Basic',
        label: 'Docker 상세',
        file: '05-class-empty-instructor.png',
      },
      {
        path: '/classes/class-linux-networking',
        selector: '.class-overview-card',
        text: 'Linux Networking',
        label: '학생 Class 상세',
        file: '06-class-empty-student.png',
      },
      {
        path: '/classes/class-docker-basic/provision',
        selector: 'main',
        text: '새 환경 생성',
        label: 'Provision',
        file: '07-provision-select.png',
      },
      {
        path: '/lab-executions/execution-kubernetes-basic',
        selector: 'main',
        text: '실습 운영 상태',
        label: 'LabExecution',
        file: '08-lab-execution.png',
      },
      {
        path: '/lab-specs',
        selector: 'main',
        text: '실습 정의',
        label: '실습 정의 목록',
        file: '09-lab-specs.png',
      },
    ]

    for (const screen of screens) {
      await navigateAuthenticated(cdp, screen)
      await capture(cdp, screen.file)
    }

    await captureProvisionOperationFlow(cdp)

    const extraScreens = [
      {
        path: '/lab-specs/new',
        selector: 'form.labspec-form',
        text: '새 실습 정의',
        label: '새 실습 정의',
        file: '12-lab-spec-new.png',
      },
      {
        path: '/lab-specs/lab-spec-kubernetes-basic',
        selector: 'form.labspec-form',
        text: 'Kubernetes Basic',
        label: '실습 정의 편집',
        file: '13-lab-spec-edit.png',
      },
      {
        path: '/lab-specs/lab-spec-linux-networking',
        selector: 'form.labspec-form',
        text: '다른 강사가 소유한 실습 정의입니다.',
        label: '실습 정의 읽기 전용',
        file: '14-lab-spec-readonly.png',
      },
      {
        path: '/not-found',
        selector: '.standalone-state-card',
        text: '페이지를 찾을 수 없습니다.',
        label: 'Not Found',
        file: '15-not-found.png',
      },
    ]

    for (const screen of extraScreens) {
      await navigateAuthenticated(cdp, screen)
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
