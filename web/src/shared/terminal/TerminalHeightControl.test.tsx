import { fireEvent, render, screen } from '@testing-library/react'
import { useState, type CSSProperties } from 'react'
import { describe, expect, it } from 'vitest'

import {
  DEFAULT_TERMINAL_HEIGHT,
  MAX_TERMINAL_HEIGHT,
  MIN_TERMINAL_HEIGHT,
  TerminalHeightControl,
} from './TerminalHeightControl'

function HeightHarness() {
  const [height, setHeight] = useState(DEFAULT_TERMINAL_HEIGHT)
  return (
    <div
      data-testid="workspace-grid"
      style={{ '--workspace-terminal-height': `${height}px` } as CSSProperties}
    >
      <TerminalHeightControl height={height} onHeightChange={setHeight} />
    </div>
  )
}

describe('Terminal height control', () => {
  it('changes the workspace grid height CSS variable with the slider', () => {
    render(<HeightHarness />)
    const slider = screen.getByRole('slider', { name: '터미널 높이' })
    expect(slider).toHaveAttribute('min', String(MIN_TERMINAL_HEIGHT))
    expect(slider).toHaveAttribute('max', String(MAX_TERMINAL_HEIGHT))
    expect(screen.getByTestId('workspace-grid')).toHaveStyle('--workspace-terminal-height: 360px')

    fireEvent.change(slider, { target: { value: '600' } })
    expect(slider).toHaveValue('600')
    expect(screen.getByTestId('workspace-grid')).toHaveStyle('--workspace-terminal-height: 600px')
    expect(screen.getByText('600px')).toBeInTheDocument()
  })

  it('supports accessible smaller/larger buttons and disables them at bounds', () => {
    render(<HeightHarness />)
    const smaller = screen.getByRole('button', { name: '터미널 높이 줄이기' })
    const larger = screen.getByRole('button', { name: '터미널 높이 늘리기' })

    fireEvent.click(smaller)
    expect(screen.getByText('320px')).toBeInTheDocument()
    expect(smaller).toBeDisabled()
    fireEvent.click(larger)
    expect(screen.getByText('360px')).toBeInTheDocument()

    fireEvent.change(screen.getByRole('slider', { name: '터미널 높이' }), {
      target: { value: String(MIN_TERMINAL_HEIGHT) },
    })
    expect(smaller).toBeDisabled()
    expect(larger).toBeEnabled()

    fireEvent.change(screen.getByRole('slider', { name: '터미널 높이' }), {
      target: { value: String(MAX_TERMINAL_HEIGHT) },
    })
    expect(smaller).toBeEnabled()
    expect(larger).toBeDisabled()
  })
})
