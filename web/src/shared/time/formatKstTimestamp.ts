// The Connector API reports UTC/offset-aware instants. Do not interpret
// zone-less values as the browser's local time.
const formatter = new Intl.DateTimeFormat('en-US', {
  timeZone: 'Asia/Seoul',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hourCycle: 'h23',
})

const rfc3339WithZone =
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/i

/** Display a server-observed instant in Korea time, without changing status. */
export function formatKstTimestamp(value: string | null | undefined): string {
  if (!value) return '확인 불가'
  const input = value.trim()
  if (!rfc3339WithZone.test(input)) return '확인 불가'

  const instant = Date.parse(input)
  if (!Number.isFinite(instant)) return '확인 불가'

  const parts = formatter.formatToParts(new Date(instant))
  const read = (type: 'year' | 'month' | 'day' | 'hour' | 'minute') =>
    parts.find((part) => part.type === type)?.value ?? ''

  return read('year') + '-' + read('month') + '-' + read('day') +
    ' ' + read('hour') + ':' + read('minute') + ' KST'
}
