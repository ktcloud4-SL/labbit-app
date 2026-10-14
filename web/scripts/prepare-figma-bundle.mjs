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
      env: { ...process.env, LABBIT_CAPTURE_EDITABLE: '1' },
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
    schemaVersion: 2,
    editableDataFile: 'editable-dom.json',
    project: 'Labbit',
    source: 'npm run capture:figma',
    captureMode: 'mock',
    generatedAt,
    viewport: { width: 1600, height: 1000 },
    pageName: `Labbit UX Review · ${generatedAt.slice(0, 10)}`,
    prototypeHints: [
      { sourceFile: '01-login.png', controlText: '로그인', targetFile: '02-class-list.png' },
      { sourceFile: '07-provision-select.png', controlText: '생성 내용 확인', targetFile: '10-provision-confirm.png' },
      { sourceFile: '10-provision-confirm.png', controlText: '다시 선택', targetFile: '07-provision-select.png' },
      { sourceFile: '10-provision-confirm.png', controlText: '환경 생성 시작', targetFile: '11-operation.png' },
      { sourceFile: '08-lab-execution.png', controlText: '전체 실습 정리', targetFile: '16-cleanup-confirm.png' },
      { sourceFile: '08-lab-execution.png', controlText: '초기화', targetFile: '17-reset-confirm.png' },
      { sourceFile: '16-cleanup-confirm.png', controlText: '취소', targetFile: '08-lab-execution.png' },
      { sourceFile: '17-reset-confirm.png', controlText: '취소', targetFile: '08-lab-execution.png' },
    ],
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
          { file: '04-lab-workspace.png', title: 'Workspace 기본 Shell', route: '/classes/class-kubernetes-basic/lab', jira: ['LBT-26'], status: 'consumer-implemented-e2e-pending' },
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
          { file: '14-lab-spec-forbidden.png', title: '실습 정의 접근 거부', route: '/lab-specs/lab-spec-linux-networking', jira: ['LBT-53'], status: 'implemented-consumer' },
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
        note: 'Terminal 연결 중 Frontend UI 상태를 Mock으로 확인한다. 실제 WSS/VM 통합 E2E는 미검증이다.',
      },
      {
        groupId: 'workspace',
        title: 'Terminal · 연결됨',
        jira: ['LBT-26'],
        note: 'Terminal consumer 코드는 구현됐지만 실제 READY VM PTY 입출력은 아직 Browser E2E로 검증하지 않았다.',
      },
      {
        groupId: 'workspace',
        title: 'Terminal · 새로고침 후 재연결 중',
        jira: ['LBT-26', 'LBT-27'],
        note: 'same-tab sessionStorage 및 60초 재연결 UI 로직은 구현됐지만 실제 PTY re-attach는 미검증이다.',
      },
      {
        groupId: 'workspace',
        title: 'Terminal · 재연결 성공',
        jira: ['LBT-27'],
        note: '기존 PTY 이어붙이기 UX는 구현돼 있지만 실제 WSS reconnect E2E가 필요하다. 과거 OUTPUT replay는 범위 밖이다.',
      },
      {
        groupId: 'workspace',
        title: 'Terminal · 복구 불가 / 종료',
        jira: ['LBT-27'],
        note: '종료·만료·stale generation 처리 UX를 Mock으로 점검한다. 실제 Connector 응답을 검증한 것은 아니다.',
      },
      {
        groupId: 'workspace',
        title: 'File / Preview · 실제 연결 대기',
        jira: ['LBT-26'],
        note: 'File consumer는 구현됐지만 실제 SFTP/VM E2E는 미검증이다. Preview는 실환경 통합을 별도 확인해야 한다.',
      },
    ],
  }
}

async function main() {
  console.log('Labbit Figma bundle 준비 시작')
  console.log('1/2 UI 자동 캡처 실행')
  await runCapture()

  console.log('2/2 Figma import manifest 생성')
  // 기존 스크린샷과 Editable DOM이 함께 생성됐는지 확인 후 manifest를 작성한다.
  const { readFile } = await import('node:fs/promises')
  const editable = JSON.parse(await readFile(path.join(outputDir, 'editable-dom.json'), 'utf8'))
  const expected = captureManifest().groups.flatMap((group) => group.screens.map((screen) => screen.file))
  const actual = new Set(editable.screens?.map((screen) => screen.file) ?? [])
  const missing = expected.filter((name) => !actual.has(name))
  if (missing.length) throw new Error('Editable DOM 캡처 누락: ' + missing.join(', '))
  const manifestPath = path.join(outputDir, 'manifest.json')
  await writeFile(manifestPath, JSON.stringify(captureManifest(), null, 2) + '\n')
  console.log(`✓ ${manifestPath}`)
  console.log('\n완료: Figma plugin에서 ui-captures 폴더의 manifest.json, editable-dom.json, PNG 전체를 함께 선택하세요.')
}

main().catch((error) => {
  console.error(`\nFigma bundle 준비 실패\n${error instanceof Error ? error.message : error}`)
  process.exitCode = 1
})
