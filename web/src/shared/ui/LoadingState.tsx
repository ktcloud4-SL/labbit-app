interface LoadingStateProps {
  label?: string
}

export function LoadingState({ label = '불러오는 중...' }: LoadingStateProps) {
  return <p role="status">{label}</p>
}
