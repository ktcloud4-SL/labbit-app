interface LoadingStateProps {
  label?: string
}

export function LoadingState({ label = '불러오는 중...' }: LoadingStateProps) {
  return (
    <div className="state-card state-card-loading" role="status">
      <span className="loading-dot" aria-hidden="true" />
      <span>{label}</span>
    </div>
  )
}
