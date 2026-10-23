// Reserve space for the toolbar, status line, and xterm minimum viewport.
export const MIN_TERMINAL_HEIGHT = 320
export const MAX_TERMINAL_HEIGHT = 760
export const DEFAULT_TERMINAL_HEIGHT = 360
export const TERMINAL_HEIGHT_STEP = 40

interface TerminalHeightControlProps {
  height: number
  onHeightChange(height: number): void
}

export function TerminalHeightControl({ height, onHeightChange }: TerminalHeightControlProps) {
  const changeHeight = (value: number) => {
    onHeightChange(Math.max(MIN_TERMINAL_HEIGHT, Math.min(MAX_TERMINAL_HEIGHT, value)))
  }

  return (
    <div className="terminal-height-control" aria-label="터미널 높이 조절">
      <span>높이</span>
      <button
        type="button"
        aria-label="터미널 높이 줄이기"
        disabled={height <= MIN_TERMINAL_HEIGHT}
        onClick={() => changeHeight(height - TERMINAL_HEIGHT_STEP)}
      >−</button>
      <input
        type="range"
        aria-label="터미널 높이"
        min={MIN_TERMINAL_HEIGHT}
        max={MAX_TERMINAL_HEIGHT}
        step={TERMINAL_HEIGHT_STEP}
        value={height}
        onChange={(event) => changeHeight(Number(event.target.value))}
      />
      <button
        type="button"
        aria-label="터미널 높이 늘리기"
        disabled={height >= MAX_TERMINAL_HEIGHT}
        onClick={() => changeHeight(height + TERMINAL_HEIGHT_STEP)}
      >+</button>
      <output aria-label="현재 터미널 높이">{height}px</output>
    </div>
  )
}
