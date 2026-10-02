import { spawn } from 'node:child_process'
import { writeFile } from 'node:fs/promises'
import path from 'node:path'

const outputDir = path.resolve(process.env.LABBIT_CAPTURE_DIR ?? 'ui-captures')

function runCapture() {
  return new Promise((resolve, reject) => {
    // Windows에서 .cmd 파일을 child_process.spawn으로 직접 실행하면
    // Node/환경 조합에 따라 EINVAL이 날 수 있습니다.
    // npm을 한 번 더 띄우지 않고 같은 Node 런타임으로 capture script를 직접 실행합니다.
    const captureScript = path.resolve('scripts', 'capture-ui.mjs')
    const child = spawn(process.execPath, [captureScript], {
      cwd: process.cwd(),
      env: process.env,
      stdio: 'inherit',
      windowsHide: true,
    })

    child.once('error', reject)
    child.once('exit', (code) => {
      if (code === 0) resolve()
      else reject(new Error(`capture:ui가 종료 코드 ${code}로 실패했습니다.`))
    })
  })
}

function captureManifest() {
  const generatedAt = new Date().toISOString()

  return {
    schemaVersion: 1,
    project: 'Labbit',
    source: 'npm run capture:figma',
    captureMode: 'mock',
    generatedAt,
    viewport: { width: 1600, height: 1000 },
    pageName: `Labbit UX Review · ${generatedAt.slice(0, 10)}`,
    groups: [
      {
        id: 'auth-class',
        title: '01 · 로그인 / 수업',
        screens: [
          { file: '01-login.png', title: '로그인', route: '/login', jira: ['LBT-74'], status: 'implemented' },
          { file: '02-class-list.png', title: '수업 목록', route: '/classes', jira: ['LBT-74'], status: 'implemented' },
          { file: '03-class-active.png', title: '진행 중인 수업 상세', route: '/classes/class-kubernetes-basic', jira: ['LBT-74'], status: 'implemented' },
          { file: '05-class-empty-instructor.png', title: '강사 · 실습 미생성 수업', route: '/classes/class-docker-basic', jira: ['LBT-74'], status: 'implemented' },
          { file: '06-class-empty-student.png', title: '학생 · 실습 미생성 수업', route: '/classes/class-linux-networking', jira: ['LBT-74'], status: 'implemented' },
        ],
      },
      {
        id: 'workspace',
        title: '02 · Workspace',
        screens: [
          { file: '04-lab-workspace.png', title: 'Workspace 기본 Shell', route: '/classes/class-kubernetes-basic/lab', jira: ['LBT-26'], status: 'implemented-shell' },
        ],
      },
      {
        id: 'provision-operation',
        title: '03 · 환경 생성 / 운영',
        screens: [
          { file: '07-provision-select.png', title: '환경 생성 대상 선택', route: '/classes/class-docker-basic/provision', jira: ['LBT-53'], status: 'implemented' },
          { file: '10-provision-confirm.png', title: '환경 생성 확인', route: '/classes/class-docker-basic/provision', jira: ['LBT-53'], status: 'implemented' },
          { file: '11-operation.png', title: 'Operation 진행 상태', route: '/operations/:operationId', jira: ['LBT-53'], status: 'implemented-mock' },
          { file: '08-lab-execution.png', title: '실습 운영 상태', route: '/lab-executions/execution-kubernetes-basic', jira: ['LBT-53'], status: 'implemented-mock' },
          { file: '16-cleanup-confirm.png', title: '전체 실습 정리 확인', route: '/lab-executions/execution-kubernetes-basic', jira: ['LBT-53'], status: 'implemented-mock' },
          { file: '17-reset-confirm.png', title: '학생 환경 초기화 확인', route: '/lab-executions/execution-kubernetes-basic', jira: ['LBT-53'], status: 'implemented-mock' },
        ],
      },
      {
        id: 'labspec',
        title: '04 · LabSpec',
        screens: [
          { file: '09-lab-specs.png', title: '실습 정의 목록', route: '/lab-specs', jira: ['LBT-53'], status: 'implemented-consumer' },
          { file: '12-lab-spec-new.png', title: '새 실습 정의', route: '/lab-specs/new', jira: ['LBT-53'], status: 'implemented-consumer' },
          { file: '13-lab-spec-edit.png', title: '실습 정의 편집', route: '/lab-specs/lab-spec-kubernetes-basic', jira: ['LBT-53'], status: 'implemented-consumer' },
          { file: '14-lab-spec-readonly.png', title: '실습 정의 읽기 전용', route: '/lab-specs/lab-spec-linux-networking', jira: ['LBT-53'], status: 'implemented-consumer' },
        ],
      },
      {
        id: 'system',
        title: '05 · 공통 상태',
        screens: [
          { file: '15-not-found.png', title: '페이지 없음', route: '/not-found', jira: ['LBT-27'], status: 'implemented' },
        ],
      },
    ],
    plannedStates: [
      {
        groupId: 'workspace',
        title: 'Terminal · 연결 중',
        jira: ['LBT-26'],
        note: 'TerminalSession 생성과 WSS attach가 끝나기 전 상태. 실제 consumer 구현 시 확정한다.',
      },
      {
        groupId: 'workspace',
        title: 'Terminal · 연결됨',
        jira: ['LBT-26'],
        note: 'PTY 입출력이 가능한 정상 상태. TerminalSession과 실제 VM transport 준비 후 구현한다.',
      },
      {
        groupId: 'workspace',
        title: 'Terminal · 새로고침 후 재연결 중',
        jira: ['LBT-26', 'LBT-27'],
        note: 'MVP는 same-tab sessionStorage의 최소 credential로 60초 grace 안에서 기존 TerminalSession/PTY re-attach를 우선한다.',
      },
      {
        groupId: 'workspace',
        title: 'Terminal · 재연결 성공',
        jira: ['LBT-27'],
        note: '새 PTY를 만들지 않고 기존 PTY를 이어서 사용한다. 과거 OUTPUT replay는 제공하지 않는다.',
      },
      {
        groupId: 'workspace',
        title: 'Terminal · 복구 불가 / 종료',
        jira: ['LBT-27'],
        note: '명시적 종료, logout, token expiry, stale generation, TERMINAL_SESSION_ENDED 등에서는 저장값을 제거하고 종료 상태를 표시한다.',
      },
      {
        groupId: 'workspace',
        title: 'File / Preview · 실제 연결 대기',
        jira: ['LBT-26'],
        note: '화면 자리와 사용자 흐름만 검토하고 endpoint/state를 임의 확정하지 않는다. owning Backend/Connector 계약 반영 후 실제 consumer를 붙인다.',
      },
    ],
  }
}

async function main() {
  console.log('Labbit Figma bundle 준비 시작')
  console.log('1/2 UI 자동 캡처 실행')
  await runCapture()

  console.log('2/2 Figma import manifest 생성')
  const manifestPath = path.join(outputDir, 'manifest.json')
  await writeFile(manifestPath, JSON.stringify(captureManifest(), null, 2) + '\n')
  console.log(`✓ ${manifestPath}`)
  console.log('\n완료: Figma plugin에서 ui-captures 폴더의 manifest.json과 PNG를 함께 선택하세요.')
}

main().catch((error) => {
  console.error(`\nFigma bundle 준비 실패\n${error instanceof Error ? error.message : error}`)
  process.exitCode = 1
})
