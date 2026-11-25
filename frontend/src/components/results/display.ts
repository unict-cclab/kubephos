// Compact, human-scale formatting for the on-screen overview (KPI cards, tooltips, the
// services table). Statistics tables and exports keep the exact formatting of format.ts.

export interface DisplayValue {value: string; unit: string; text: string}

function trim(value: number, digits: number): string {
  return value.toLocaleString('en-US', {maximumFractionDigits: digits, minimumFractionDigits: 0})
}

function significant(value: number): string {
  const absolute = Math.abs(value)
  return trim(value, absolute >= 100 ? 0 : absolute >= 10 ? 1 : absolute >= 1 ? 2 : 3)
}

// axis: shorter tick labels (no unit for rates and counts).
export function displayValue(value: number | undefined, unit: string, axis = false): DisplayValue {
  const make = (number: string, suffix: string): DisplayValue => ({value: number, unit: suffix, text: suffix ? `${number}${suffix === '%' ? '' : ' '}${suffix}` : number})
  if (value === undefined || !Number.isFinite(value)) return {value: '—', unit: '', text: '—'}
  switch (unit) {
    case 'milliseconds':
      if (Math.abs(value) >= 1000) return make(trim(value / 1000, Math.abs(value) >= 10000 ? 1 : 2), 's')
      return make(trim(value, Math.abs(value) >= 10 ? 0 : 1), 'ms')
    case 'seconds':
      return Math.abs(value) < 1 ? make(trim(value * 1000, 0), 'ms') : make(trim(value, 2), 's')
    case 'requests_per_second':
      return make(significant(value), axis ? '' : 'req/s')
    case 'percent':
      return make(Math.abs(value) > 0 && Math.abs(value) < .1 ? trim(value, 3) : trim(value, Math.abs(value) >= 10 ? 0 : 1), '%')
    case 'cores':
      return Math.abs(value) < 1 && !axis ? make(trim(value * 1000, 0), 'mCPU') : make(trim(value, 2), axis ? '' : 'cores')
    case 'bytes': {
      const mib = value / 1024 ** 2
      return mib >= 1024 ? make(trim(mib / 1024, 2), 'GiB') : make(trim(mib, mib >= 100 ? 0 : 1), 'MiB')
    }
    case 'bytes_per_second': {
      const mib = value / 1024 ** 2
      return mib >= 1 ? make(trim(mib, 1), 'MiB/s') : make(trim(value / 1024, 1), 'KiB/s')
    }
    case 'replicas':
    case 'count':
      return make(significant(value), '')
    default:
      return make(significant(value), '')
  }
}

// m:ss (or h:mm:ss) for a duration in seconds.
export function formatClock(seconds: number): string {
  const total = Math.max(0, Math.round(seconds))
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor(total % 3600 / 60)
  const rest = String(total % 60).padStart(2, '0')
  return hours ? `${hours}:${String(minutes).padStart(2, '0')}:${rest}` : `${minutes}:${rest}`
}

// Signed relative change, e.g. "+12.3 %" / "−4 %".
export function formatChange(percent: number | undefined): string {
  if (percent === undefined || !Number.isFinite(percent)) return '—'
  const absolute = Math.abs(percent)
  const digits = absolute >= 10 ? 0 : 1
  return `${percent > 0 ? '+' : percent < 0 ? '−' : '±'}${trim(absolute, digits)} %`
}

export function formatSpan(milliseconds: number): string {
  const minutes = Math.round(milliseconds / 60000)
  if (minutes >= 90) return `${trim(milliseconds / 3600000, 1)} h`
  if (minutes >= 1) return `${minutes} min`
  return `${Math.round(milliseconds / 1000)} s`
}

// Table numbers: three significant digits with thousands separators (1,720 / 61.2 / 0.0154),
// integers above 1,000 rounded to the unit. Unitless — the unit sits in the column header.
export function formatSignificant(value: number | undefined, digits = 3): string {
  if (value === undefined || !Number.isFinite(value)) return '—'
  if (value === 0) return '0'
  const absolute = Math.abs(value)
  const text = absolute >= 10 ** digits ? absolute.toLocaleString('en-US', {maximumFractionDigits: 0}) : absolute.toLocaleString('en-US', {maximumSignificantDigits: digits})
  return value < 0 ? `−${text}` : text
}

// Whole counts with thousands separators.
export function formatCount(value: number | undefined): string {
  if (value === undefined || !Number.isFinite(value)) return '—'
  return Math.round(value).toLocaleString('en-US')
}
