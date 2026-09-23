import { useEffect, useId, useRef } from 'react'
import type { RefObject } from 'react'

interface ConfirmationDialogProps {
  title: string
  description: string
  confirmLabel: string
  pendingLabel?: string
  cancelLabel?: string
  tone?: 'default' | 'danger'
  pending?: boolean
  confirmDisabled?: boolean
  errorMessage?: string | null
  returnFocusRef?: RefObject<HTMLElement | null>
  onCancel: () => void
  onConfirm: () => void
}

const focusableSelector =
  'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

export function ConfirmationDialog({
  title,
  description,
  confirmLabel,
  pendingLabel = '요청 중...',
  cancelLabel = '취소',
  tone = 'default',
  pending = false,
  confirmDisabled = false,
  errorMessage,
  returnFocusRef,
  onCancel,
  onConfirm,
}: ConfirmationDialogProps) {
  const titleId = useId()
  const descriptionId = useId()
  const dialogRef = useRef<HTMLElement | null>(null)
  const cancelButtonRef = useRef<HTMLButtonElement | null>(null)

  useEffect(() => {
    const returnFocusElement = returnFocusRef?.current

    return () => {
      returnFocusElement?.focus()
    }
  }, [returnFocusRef])

  useEffect(() => {
    if (pending) {
      dialogRef.current?.focus()
      return
    }

    cancelButtonRef.current?.focus()
  }, [pending])

  return (
    <div className="modal-backdrop">
      <section
        ref={dialogRef}
        className={`modal-card ${tone === 'danger' ? 'modal-card-danger' : ''}`}
        role="dialog"
        aria-modal="true"
        aria-busy={pending}
        tabIndex={-1}
        aria-labelledby={titleId}
        aria-describedby={descriptionId}
        onKeyDown={(event) => {
          if (event.key === 'Escape') {
            event.preventDefault()

            if (!pending) {
              onCancel()
            }
            return
          }

          if (event.key !== 'Tab') return

          const focusableElements = Array.from(
            event.currentTarget.querySelectorAll<HTMLElement>(focusableSelector),
          )

          if (focusableElements.length === 0) {
            event.preventDefault()
            dialogRef.current?.focus()
            return
          }

          const first = focusableElements[0]
          const last = focusableElements[focusableElements.length - 1]

          if (event.shiftKey && document.activeElement === first) {
            event.preventDefault()
            last.focus()
          } else if (!event.shiftKey && document.activeElement === last) {
            event.preventDefault()
            first.focus()
          }
        }}
      >
        <p className="eyebrow">최종 확인</p>
        <h2 id={titleId}>{title}</h2>
        <p id={descriptionId} className="muted">
          {description}
        </p>

        {errorMessage && (
          <p className="form-error" role="alert">
            {errorMessage}
          </p>
        )}

        <div className="form-actions">
          <button
            ref={cancelButtonRef}
            className="secondary-button"
            type="button"
            disabled={pending}
            onClick={onCancel}
          >
            {cancelLabel}
          </button>
          <button
            className="primary-button"
            type="button"
            disabled={pending || confirmDisabled}
            onClick={onConfirm}
          >
            {pending ? pendingLabel : confirmLabel}
          </button>
        </div>
      </section>
    </div>
  )
}
