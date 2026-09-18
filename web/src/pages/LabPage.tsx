import { Link, useParams } from 'react-router-dom'

export function LabPage() {
  const { classId } = useParams()

  return (
    <main>
      <h1>Lab Workspace</h1>
      <p>Class: {classId ?? 'unknown'}</p>
      <p>Editor, Terminal, Preview 실제 통합은 후속 Story에서 진행합니다.</p>
      <Link to="/classes">Class 목록으로 돌아가기</Link>
    </main>
  )
}
