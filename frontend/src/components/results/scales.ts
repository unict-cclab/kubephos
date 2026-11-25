import {niceAxis} from '../../charts'

export interface Scale {
  lower: number
  upper: number
  ticks: number[]
  log: boolean
  // Maps a data value to [0, 1] along the axis; undefined when not representable (≤ 0 on log).
  position: (value: number) => number | undefined
}

const candidates = [1, 1.25, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]

function niceStep(range: number, count = 5): number {
  const raw = range / count
  const magnitude = 10 ** Math.floor(Math.log10(raw))
  return (candidates.find(candidate => candidate >= raw / magnitude * .98) ?? 10) * magnitude
}

// Linear axis over [min, max]. From zero (default) it matches niceAxis; otherwise the
// domain is padded to the nearest ticks around the data.
export function linearScale(minimum: number, maximum: number, {zero = true, integer = false}: {zero?: boolean; integer?: boolean} = {}): Scale {
  let lower = 0
  let upper: number
  let ticks: number[]
  const finiteMax = Number.isFinite(maximum) ? maximum : 1
  const finiteMin = Number.isFinite(minimum) ? minimum : 0
  if (zero) {
    const axis = niceAxis(Math.max(finiteMax, finiteMin < 0 ? -finiteMin : 0), integer)
    lower = Math.min(0, finiteMin)
    upper = axis.upper
    ticks = axis.ticks
    if (lower < 0) {
      const step = ticks[1] - ticks[0]
      lower = Math.floor(lower / step) * step
      ticks = [...Array.from({length: Math.round(-lower / step)}, (_, index) => lower + index * step), ...ticks]
    }
  } else {
    const range = finiteMax - finiteMin || Math.abs(finiteMax) || 1
    let step = niceStep(range)
    if (integer) step = Math.max(1, Math.ceil(step))
    lower = Math.floor(finiteMin / step) * step
    upper = Math.ceil(finiteMax / step) * step
    if (upper === lower) upper = lower + step
    ticks = []
    for (let tick = lower; tick <= upper + step * 1e-9; tick += step) ticks.push(Number(tick.toPrecision(12)))
  }
  return {lower, upper, ticks, log: false, position: value => Number.isFinite(value) ? (value - lower) / (upper - lower) : undefined}
}

// Base-10 log axis covering whole decades; ticks at 1-2-5 within at most 3 decades.
export function logScale(minimum: number, maximum: number): Scale {
  const positive = [minimum, maximum].filter(value => Number.isFinite(value) && value > 0)
  const low = positive.length ? Math.min(...positive) : 1
  const high = positive.length ? Math.max(...positive) : 10
  const lowerExponent = Math.floor(Math.log10(low))
  let upperExponent = Math.ceil(Math.log10(high))
  if (upperExponent === lowerExponent) upperExponent++
  const lower = 10 ** lowerExponent
  const upper = 10 ** upperExponent
  const decades = upperExponent - lowerExponent
  const ticks: number[] = []
  for (let exponent = lowerExponent; exponent <= upperExponent; exponent++) {
    for (const factor of decades <= 3 ? [1, 2, 5] : [1]) {
      const tick = Number((factor * 10 ** exponent).toPrecision(12))
      if (tick >= lower && tick <= upper) ticks.push(tick)
    }
  }
  return {lower, upper, ticks, log: true, position: value => value > 0 && Number.isFinite(value) ? (Math.log10(value) - lowerExponent) / decades : undefined}
}

// Extremes with a loop: Math.min(...values) throws a RangeError on long runs
// (more arguments than the engine allows, ~65k in Safari, ~120k in Chrome).
export function extent(values: Iterable<number>): {min: number; max: number; minPositive: number; count: number} {
  let min = Infinity
  let max = -Infinity
  let minPositive = Infinity
  let count = 0
  for (const value of values) {
    if (!Number.isFinite(value)) continue
    count++
    if (value < min) min = value
    if (value > max) max = value
    if (value > 0 && value < minPositive) minPositive = value
  }
  return {min, max, minPositive, count}
}

export function valueScale(values: number[], {zero, log, integer}: {zero: boolean; log: boolean; integer?: boolean}): Scale {
  const range = extent(values)
  if (log) return logScale(range.minPositive, range.max)
  return linearScale(range.count ? range.min : 0, range.count ? range.max : 1, {zero, integer})
}
