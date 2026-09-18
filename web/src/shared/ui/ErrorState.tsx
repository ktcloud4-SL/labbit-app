interface ErrorStateProps {
  message?: string
}

export function ErrorState({
  message = '요청을 처리하지 못했습니다. 잠시 후 다시 시도해 주세요.',
}: ErrorStateProps) {
  return <p role="alert">{message}</p>
}
