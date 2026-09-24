interface ErrorStateProps {
  message?: string
}

export function ErrorState({
  message = '요청을 처리하지 못했습니다. 잠시 후 다시 시도해 주세요.',
}: ErrorStateProps) {
  return (
    <div className="state-card state-card-error" role="alert">
      <span className="state-card-mark" aria-hidden="true">
        !
      </span>
      <div>
        <strong>요청을 완료하지 못했습니다.</strong>
        <p>{message}</p>
      </div>
    </div>
  )
}
