// The Connector API reports UTC/offset-aware instants. Do not interpret
// zone-less values as the browser's local time.
const formatter = new Intl.DateTimeFormat('en-US', {
  timeZone: 'Asia/Seoul',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hourCycle: 'h23',
})

const rfc3339WithZone =
  /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/i

// Date.parse accepts some nonexistent calendar dates by normalizing them
// (e.g. February 30 -> March 2). Reject those instead of displaying a lie.
function validCalendarInstant(parts: RegExpMatchArray): boolean {
  const year = Number(parts[1])
  const month = Number(parts[2])
  const day = Number(parts[3])
  const hour = Number(parts[4])
  const minute = Number(parts[5])
  const second = Number(parts[6])
  if (year < 1 || month < 1 || month > 12 || hour > 23 || minute > 59 || second > 59) {
    return false
  }
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0)
  const daysInMonth = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31]
  return day >= 1 && day <= daysInMonth[month - 1]
}

/** Display a server-observed instant in Korea time, without changing status. */
export function formatKstTimestamp(value: string | null | undefined): string {
  if (!value) return '확인 불가'
  const input = value.trim()
  const parsed = input.match(rfc3339WithZone)
  if (!parsed || !validCalendarInstant(parsed)) return '확인 불가'

  const instant = Date.parse(input)
  if (!Number.isFinite(instant)) return '확인 불가'

  const parts = formatter.formatToParts(new Date(instant))
  const read = (type: 'year' | 'month' | 'day' | 'hour' | 'minute' | 'second') =>
    parts.find((part) => part.type === type)?.value ?? ''

  return read('year') + '-' + read('month') + '-' + read('day') +
    ' ' + read('hour') + ':' + read('minute') + ':' + read('second') + ' KST'
}
