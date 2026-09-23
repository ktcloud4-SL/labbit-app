import { fireEvent, render, screen } from '@testing-library/react'
import { useRef, useState } from 'react'
import { describe, expect, it, vi } from 'vitest'

import { ConfirmationDialog } from './ConfirmationDialog'

interface TestHostProps {
  pending?: boolean
  confirmDisabled?: boolean
}

function TestHost({
  pending = false,
  confirmDisabled = false,
}: TestHostProps) {
  const [open, setOpen] = useState(false)
  const triggerRef = useRef<HTMLButtonElement | null>(null)

  return (
    <>
      <button ref={triggerRef} type="button" onClick={() => setOpen(true)}>
        위험 작업 열기
      </button>
      {open && (
        <ConfirmationDialog
          title="작업을 실행할까요?"
          description="이 작업은 현재 상태를 변경합니다."
          confirmLabel="실행"
          pending={pending}
          confirmDisabled={confirmDisabled}
          returnFocusRef={triggerRef}
          onCancel={() => setOpen(false)}
          onConfirm={vi.fn()}
        />
      )}
    </>
  )
}

describe('ConfirmationDialog', () => {
  it('열릴 때 취소에 focus하고 Escape로 닫은 뒤 trigger로 focus를 복원한다', () => {
    render(<TestHost />)

    const trigger = screen.getByRole('button', { name: '위험 작업 열기' })
    fireEvent.click(trigger)

    const cancelButton = screen.getByRole('button', { name: '취소' })
    expect(cancelButton).toHaveFocus()

    fireEvent.keyDown(cancelButton, { key: 'Escape' })

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(trigger).toHaveFocus()
  })

  it('Tab과 Shift+Tab focus를 dialog 내부에서 순환시킨다', () => {
    render(<TestHost />)

    fireEvent.click(screen.getByRole('button', { name: '위험 작업 열기' }))

    const cancelButton = screen.getByRole('button', { name: '취소' })
    const confirmButton = screen.getByRole('button', { name: '실행' })

    fireEvent.keyDown(cancelButton, { key: 'Tab', shiftKey: true })
    expect(confirmButton).toHaveFocus()

    fireEvent.keyDown(confirmButton, { key: 'Tab' })
    expect(cancelButton).toHaveFocus()
  })

  it('pending 중에는 dialog에 focus를 유지하고 Escape로 닫히지 않는다', () => {
    const { rerender } = render(<TestHost />)

    fireEvent.click(screen.getByRole('button', { name: '위험 작업 열기' }))
    rerender(<TestHost pending />)

    const dialog = screen.getByRole('dialog')
    expect(dialog).toHaveAttribute('aria-busy', 'true')
    expect(dialog).toHaveFocus()
    expect(screen.getByRole('button', { name: '취소' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '요청 중...' })).toBeDisabled()

    fireEvent.keyDown(dialog, { key: 'Escape' })
    expect(screen.getByRole('dialog')).toBeInTheDocument()

    fireEvent.keyDown(dialog, { key: 'Tab' })
    expect(dialog).toHaveFocus()
  })
})
