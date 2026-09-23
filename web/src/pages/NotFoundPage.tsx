import { Link } from 'react-router-dom'

export function NotFoundPage() {
  return (
    <main className="standalone-state-page">
      <section className="standalone-state-card">
        <span className="state-code" aria-hidden="true">
          404
        </span>
        <p className="eyebrow">Page not found</p>
        <h1>페이지를 찾을 수 없습니다.</h1>
        <p className="muted">
          주소가 잘못되었거나 이동된 페이지일 수 있습니다. 수업 목록에서 다시 시작해 주세요.
        </p>
        <Link className="primary-link standalone-state-action" to="/classes">
          수업 목록으로 이동
        </Link>
      </section>
    </main>
  )
}
