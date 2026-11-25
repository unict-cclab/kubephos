// Deterministic, dependency-free statistics for KubePhos results.
//
// Every function here is a pure function of its inputs: no Map iteration order, locale,
// clock or Math.random is involved, so the same datasets always produce the same numbers.
//
// Method choices (see statistics.test.ts for reference values from SciPy 1.x):
// - Student t: CDF through the regularized incomplete beta function, I_x(a, b), evaluated
//   with the modified Lentz continued fraction (Press et al., Numerical Recipes, 3rd ed.,
//   §6.4). Quantiles invert the CDF with safeguarded Newton iterations inside a bisection
//   bracket, so they hold for any real df > 0 (Welch–Satterthwaite df are not integers).
// - Normal quantile: Acklam's rational approximation refined with Halley steps on the
//   exact CDF (erfc via the regularized incomplete gamma function).
// - Welch's unequal-variance t-test with Welch–Satterthwaite df (Welch 1947).
// - Hedges' g with the EXACT small-sample correction J(df) = Γ(df/2) / (√(df/2) Γ((df−1)/2))
//   (Hedges 1981) and its large-sample variance V(g) = J²·((n1+n2)/(n1·n2) + d²/(2(n1+n2)))
//   (Hedges & Olkin 1985; Borenstein et al. 2009, eq. 4.20–4.24); the CI is g ± z·√V(g).
// - Holm (1979) step-down adjustment, which controls the family-wise error rate.
// - Mann–Whitney U: exact permutation distribution when there are no ties (n1, n2 ≤ 50);
//   otherwise the normal approximation with tie and continuity correction (as SciPy).
// - Bootstrap percentile CI of the median (Efron & Tibshirani 1993, §13.3) with the seeded
//   mulberry32 PRNG, so the interval is reproducible.
// - Effective sample size of an autocorrelated series under an AR(1) model:
//   n_eff = n·(1−ρ1)/(1+ρ1) with the lag-1 sample autocorrelation ρ1 (Wilks 2011,
//   "Statistical Methods in the Atmospheric Sciences", eq. 5.12; Zwiers & von Storch 1995),
//   clamped to [2, n]. Rolling-window rates sampled more often than their window are
//   strongly positively autocorrelated; ignoring it gives CIs that are far too narrow.
//   AR(1) is chosen over batch means because runs are short (often < 100 samples), where
//   batch means has too few batches to estimate the variance stably.

const EPSILON = 1e-15
const TINY = 1e-300
const MAX_ITERATIONS = 10000

const lanczos = [0.99999999999980993, 676.5203681218851, -1259.1392167224028, 771.32342877765313, -176.61502916214059, 12.507343278686905, -0.13857109526572012, 9.9843695780195716e-6, 1.5056327351493116e-7]

// ln Γ(x) for x > 0 (Lanczos approximation, g = 7, n = 9; relative error ≈ 1e-15).
export function logGamma(x: number): number {
  if (x < 0.5) return Math.log(Math.PI / Math.abs(Math.sin(Math.PI * x))) - logGamma(1 - x)
  const shifted = x - 1
  let sum = lanczos[0]
  for (let index = 1; index < lanczos.length; index++) sum += lanczos[index] / (shifted + index)
  const t = shifted + 7.5
  return 0.5 * Math.log(2 * Math.PI) + (shifted + 0.5) * Math.log(t) - t + Math.log(sum)
}

function betaContinuedFraction(x: number, a: number, b: number): number {
  const sum = a + b
  const plus = a + 1
  const minus = a - 1
  let c = 1
  let d = 1 - sum * x / plus
  if (Math.abs(d) < TINY) d = TINY
  d = 1 / d
  let fraction = d
  for (let m = 1; m <= MAX_ITERATIONS; m++) {
    const twice = 2 * m
    let numerator = m * (b - m) * x / ((minus + twice) * (a + twice))
    d = 1 + numerator * d
    if (Math.abs(d) < TINY) d = TINY
    c = 1 + numerator / c
    if (Math.abs(c) < TINY) c = TINY
    d = 1 / d
    fraction *= d * c
    numerator = -(a + m) * (sum + m) * x / ((a + twice) * (plus + twice))
    d = 1 + numerator * d
    if (Math.abs(d) < TINY) d = TINY
    c = 1 + numerator / c
    if (Math.abs(c) < TINY) c = TINY
    d = 1 / d
    const delta = d * c
    fraction *= delta
    if (Math.abs(delta - 1) < EPSILON) break
  }
  return fraction
}

// Regularized incomplete beta function I_x(a, b).
export function regularizedIncompleteBeta(x: number, a: number, b: number): number {
  if (!(a > 0) || !(b > 0) || Number.isNaN(x)) return NaN
  if (x <= 0) return 0
  if (x >= 1) return 1
  const front = Math.exp(logGamma(a + b) - logGamma(a) - logGamma(b) + a * Math.log(x) + b * Math.log1p(-x))
  if (x < (a + 1) / (a + b + 2)) return front * betaContinuedFraction(x, a, b) / a
  return 1 - front * betaContinuedFraction(1 - x, b, a) / b
}

// Regularized upper incomplete gamma Q(a, x), used for erfc.
function regularizedUpperGamma(a: number, x: number): number {
  if (x <= 0) return 1
  const front = Math.exp(-x + a * Math.log(x) - logGamma(a))
  if (x < a + 1) {
    let term = 1 / a
    let sum = term
    for (let n = 1; n <= MAX_ITERATIONS; n++) {
      term *= x / (a + n)
      sum += term
      if (Math.abs(term) < Math.abs(sum) * EPSILON) break
    }
    return 1 - front * sum
  }
  let b = x + 1 - a
  let c = 1 / TINY
  let d = 1 / b
  let fraction = d
  for (let n = 1; n <= MAX_ITERATIONS; n++) {
    const numerator = -n * (n - a)
    b += 2
    d = numerator * d + b
    if (Math.abs(d) < TINY) d = TINY
    c = b + numerator / c
    if (Math.abs(c) < TINY) c = TINY
    d = 1 / d
    const delta = d * c
    fraction *= delta
    if (Math.abs(delta - 1) < EPSILON) break
  }
  return front * fraction
}

function erfc(x: number): number {
  return x >= 0 ? regularizedUpperGamma(0.5, x * x) : 2 - regularizedUpperGamma(0.5, x * x)
}

export function normalCDF(x: number): number {
  if (Number.isNaN(x)) return NaN
  return 0.5 * erfc(-x / Math.SQRT2)
}

const acklamA = [-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02, 1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00]
const acklamB = [-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02, 6.680131188771972e+01, -1.328068155288572e+01]
const acklamC = [-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00, -2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00]
const acklamD = [7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00, 3.754408661907416e+00]

export function normalQuantile(p: number): number {
  if (!(p >= 0 && p <= 1)) return NaN
  if (p === 0) return -Infinity
  if (p === 1) return Infinity
  const low = 0.02425
  let x: number
  if (p < low || p > 1 - low) {
    const q = Math.sqrt(-2 * Math.log(p < low ? p : 1 - p))
    const value = (((((acklamC[0] * q + acklamC[1]) * q + acklamC[2]) * q + acklamC[3]) * q + acklamC[4]) * q + acklamC[5]) / ((((acklamD[0] * q + acklamD[1]) * q + acklamD[2]) * q + acklamD[3]) * q + 1)
    x = p < low ? value : -value
  } else {
    const q = p - 0.5
    const r = q * q
    x = (((((acklamA[0] * r + acklamA[1]) * r + acklamA[2]) * r + acklamA[3]) * r + acklamA[4]) * r + acklamA[5]) * q / (((((acklamB[0] * r + acklamB[1]) * r + acklamB[2]) * r + acklamB[3]) * r + acklamB[4]) * r + 1)
  }
  // Halley refinement on the exact CDF (Acklam's own refinement step).
  for (let step = 0; step < 2; step++) {
    const error = normalCDF(x) - p
    const u = error * Math.sqrt(2 * Math.PI) * Math.exp(x * x / 2)
    x -= u / (1 + x * u / 2)
  }
  return x
}

// Above this many degrees of freedom the t and normal distributions agree to < 1e-8.
const NORMAL_LIMIT = 1e7

// Two-sided tail probability P(|T| ≥ |t|) of Student's t; precise for small p-values.
export function studentTwoSidedPValue(t: number, degreesOfFreedom: number): number {
  if (Number.isNaN(t) || !(degreesOfFreedom > 0)) return NaN
  if (!Number.isFinite(t)) return 0
  if (degreesOfFreedom > NORMAL_LIMIT) return 2 * normalCDF(-Math.abs(t))
  return Math.min(1, regularizedIncompleteBeta(degreesOfFreedom / (degreesOfFreedom + t * t), degreesOfFreedom / 2, 0.5))
}

// Student t cumulative distribution function for any real df > 0.
export function studentTCDF(t: number, degreesOfFreedom: number): number {
  if (Number.isNaN(t) || !(degreesOfFreedom > 0)) return NaN
  if (t === Infinity) return 1
  if (t === -Infinity) return 0
  const tail = studentTwoSidedPValue(t, degreesOfFreedom) / 2
  return t > 0 ? 1 - tail : tail
}

function studentTDensity(t: number, degreesOfFreedom: number): number {
  return Math.exp(logGamma((degreesOfFreedom + 1) / 2) - logGamma(degreesOfFreedom / 2) - 0.5 * Math.log(degreesOfFreedom * Math.PI) - (degreesOfFreedom + 1) / 2 * Math.log1p(t * t / degreesOfFreedom))
}

// Quantile (inverse CDF) of Student's t for any real df > 0, accurate to ~1e-12.
export function studentTQuantile(p: number, degreesOfFreedom: number): number {
  if (!(p >= 0 && p <= 1) || !(degreesOfFreedom > 0)) return NaN
  if (p === 0) return -Infinity
  if (p === 1) return Infinity
  if (p === 0.5) return 0
  if (p < 0.5) return -studentTQuantile(1 - p, degreesOfFreedom)
  // Closed forms for df = 1 (Cauchy) and df = 2.
  if (degreesOfFreedom === 1) return Math.tan(Math.PI * (p - 0.5))
  if (degreesOfFreedom === 2) return (2 * p - 1) / Math.sqrt(2 * p * (1 - p))
  if (degreesOfFreedom > NORMAL_LIMIT) return normalQuantile(p)
  // Solve upper tail(t) = 1 − p for t > 0; the tail decreases monotonically in t.
  const target = 1 - p
  const tail = (t: number) => studentTwoSidedPValue(t, degreesOfFreedom) / 2
  const z = normalQuantile(p)
  // Cornish–Fisher start (Abramowitz & Stegun 26.7.5), refined below.
  const v = degreesOfFreedom
  let t = z + (z ** 3 + z) / (4 * v) + (5 * z ** 5 + 16 * z ** 3 + 3 * z) / (96 * v * v)
  if (!Number.isFinite(t) || t <= 0) t = 1
  let low = 0
  let high = t
  while (tail(high) > target && high < 1e300) high *= 2
  for (let iteration = 0; iteration < 500; iteration++) {
    const error = tail(t) - target
    if (error > 0) low = Math.max(low, t)
    else high = Math.min(high, t)
    let next = t + error / studentTDensity(t, degreesOfFreedom)
    if (!(next > low && next < high)) next = (low + high) / 2
    if (Math.abs(next - t) <= 1e-14 * Math.max(1, Math.abs(t)) || high - low <= 1e-15 * Math.max(1, high)) return next
    t = next
  }
  return t
}

// Two-sided critical value t_{1−α/2, df} of a (1 − α) confidence interval.
export function tCritical(degreesOfFreedom: number, confidence = 0.95): number {
  return studentTQuantile(1 - (1 - confidence) / 2, degreesOfFreedom)
}

export function mean(values: readonly number[]): number {
  let sum = 0
  for (const value of values) sum += value
  return sum / values.length
}

// Unbiased sample variance (n − 1 denominator); 0 when n < 2.
export function sampleVariance(values: readonly number[]): number {
  if (values.length < 2) return 0
  const center = mean(values)
  let sum = 0
  for (const value of values) sum += (value - center) ** 2
  return sum / (values.length - 1)
}

// Linear interpolation between order statistics (R type 7, NumPy default). `sorted` ascending.
export function quantileSorted(sorted: readonly number[], ratio: number): number {
  if (!sorted.length) return NaN
  if (sorted.length === 1) return sorted[0]
  const position = (sorted.length - 1) * ratio
  const lower = Math.floor(position)
  const fraction = position - lower
  return sorted[lower] + (sorted[Math.min(lower + 1, sorted.length - 1)] - sorted[lower]) * fraction
}

export interface ScientificSummary {
  count: number
  mean: number
  standardDeviation: number
  min: number
  max: number
  median: number
  p05: number
  p25: number
  p75: number
  p95: number
  p99: number
  coefficientOfVariation: number
  confidence95Low: number
  confidence95High: number
  // Present only for time-series summaries (summarizeTimeSeries): the CI uses nEffective.
  nEffective?: number
  lag1Autocorrelation?: number
}

const emptySummary: ScientificSummary = {count: 0, mean: 0, standardDeviation: 0, min: 0, max: 0, median: 0, p05: 0, p25: 0, p75: 0, p95: 0, p99: 0, coefficientOfVariation: 0, confidence95Low: 0, confidence95High: 0}

function describe(values: readonly number[], effectiveCount?: number): ScientificSummary {
  const finite = values.filter(Number.isFinite)
  const count = finite.length
  if (!count) return {...emptySummary}
  const sorted = [...finite].sort((left, right) => left - right)
  const center = mean(sorted)
  const standardDeviation = Math.sqrt(sampleVariance(sorted))
  const n = effectiveCount ?? count
  const margin = count > 1 && n > 1 ? tCritical(n - 1) * standardDeviation / Math.sqrt(n) : 0
  return {count, mean: center, standardDeviation, min: sorted[0], max: sorted[count - 1], median: quantileSorted(sorted, .5), p05: quantileSorted(sorted, .05), p25: quantileSorted(sorted, .25), p75: quantileSorted(sorted, .75), p95: quantileSorted(sorted, .95), p99: quantileSorted(sorted, .99), coefficientOfVariation: center === 0 ? 0 : standardDeviation / Math.abs(center), confidence95Low: center - margin, confidence95High: center + margin}
}

// Summary of INDEPENDENT observations (e.g. one value per repetition): exact Student t CI
// of the mean with n − 1 df. Non-finite values are ignored (unavailable, never zero).
export function summarize(values: readonly number[]): ScientificSummary {
  return describe(values)
}

// Summary of ONE run's time series, in time order. The samples are rolling-window
// aggregates and autocorrelated, so the CI of the mean uses the AR(1) effective sample
// size: mean ± t_{0.975, n_eff−1}·s/√n_eff. Descriptive statistics are unchanged.
export function summarizeTimeSeries(values: readonly number[]): ScientificSummary {
  const finite = values.filter(Number.isFinite)
  const {nEffective, rho} = effectiveSampleSize(finite)
  const summary = describe(finite, finite.length > 1 ? nEffective : undefined)
  if (!finite.length) return summary
  return rho === undefined ? {...summary, nEffective} : {...summary, nEffective, lag1Autocorrelation: rho}
}

// Lag-1 sample autocorrelation r1 = Σ(x_t−x̄)(x_{t+1}−x̄) / Σ(x_t−x̄)² (Box & Jenkins).
// Undefined for fewer than 3 samples or a constant series.
export function lag1Autocorrelation(values: readonly number[]): number | undefined {
  if (values.length < 3) return undefined
  const center = mean(values)
  let numerator = 0
  let denominator = 0
  for (let index = 0; index < values.length; index++) {
    const deviation = values[index] - center
    denominator += deviation * deviation
    if (index + 1 < values.length) numerator += deviation * (values[index + 1] - center)
  }
  return denominator > 0 ? numerator / denominator : undefined
}

// AR(1) effective sample size n·(1−ρ1)/(1+ρ1), clamped to [2, n] (n when n < 2 or ρ1 is
// undefined). Negative ρ1 would inflate n_eff above n; the clamp keeps it conservative.
export function effectiveSampleSize(values: readonly number[]): {n: number; nEffective: number; rho?: number} {
  const n = values.length
  const rho = lag1Autocorrelation(values)
  if (n < 2 || rho === undefined) return {n, nEffective: n, rho}
  const raw = n * (1 - rho) / (1 + rho)
  return {n, nEffective: Math.min(n, Math.max(2, Number.isFinite(raw) ? raw : 2)), rho}
}

export interface WelchTest {
  difference: number
  standardError: number
  t?: number
  degreesOfFreedom?: number
  pValue?: number
  low?: number
  high?: number
}

// Welch's t-test of mean(group) − mean(baseline). Needs ≥ 2 values per group. When both
// variances are 0 the test is undefined and only the difference is returned.
export function welchTTest(baseline: readonly number[], group: readonly number[], confidence = 0.95): WelchTest | undefined {
  if (baseline.length < 2 || group.length < 2) return undefined
  const difference = mean(group) - mean(baseline)
  const baselineTerm = sampleVariance(baseline) / baseline.length
  const groupTerm = sampleVariance(group) / group.length
  const standardError = Math.sqrt(baselineTerm + groupTerm)
  if (!(standardError > 0)) return {difference, standardError: 0}
  const degreesOfFreedom = (baselineTerm + groupTerm) ** 2 / (baselineTerm ** 2 / (baseline.length - 1) + groupTerm ** 2 / (group.length - 1))
  const t = difference / standardError
  const margin = tCritical(degreesOfFreedom, confidence) * standardError
  return {difference, standardError, t, degreesOfFreedom, pValue: studentTwoSidedPValue(t, degreesOfFreedom), low: difference - margin, high: difference + margin}
}

// Exact Hedges small-sample correction factor J(df).
export function hedgesCorrection(degreesOfFreedom: number): number {
  return Math.exp(logGamma(degreesOfFreedom / 2) - 0.5 * Math.log(degreesOfFreedom / 2) - logGamma((degreesOfFreedom - 1) / 2))
}

export interface HedgesG {cohensD: number; correction: number; g: number; standardError: number; low: number; high: number}

// Standardized mean difference (group − baseline) / pooled SD, with Hedges' correction.
export function hedgesG(baseline: readonly number[], group: readonly number[], confidence = 0.95): HedgesG | undefined {
  const n1 = baseline.length
  const n2 = group.length
  if (n1 < 2 || n2 < 2) return undefined
  const degreesOfFreedom = n1 + n2 - 2
  const pooled = Math.sqrt(((n1 - 1) * sampleVariance(baseline) + (n2 - 1) * sampleVariance(group)) / degreesOfFreedom)
  if (!(pooled > 0)) return undefined
  const cohensD = (mean(group) - mean(baseline)) / pooled
  const correction = hedgesCorrection(degreesOfFreedom)
  const g = correction * cohensD
  const standardError = correction * Math.sqrt((n1 + n2) / (n1 * n2) + cohensD ** 2 / (2 * (n1 + n2)))
  const z = normalQuantile(1 - (1 - confidence) / 2)
  return {cohensD, correction, g, standardError, low: g - z * standardError, high: g + z * standardError}
}

// Midranks (1-based) of the pooled sample; returns ranks and the tie groups' sizes.
function midranks(values: readonly number[]): {ranks: number[]; ties: number[]} {
  const order = values.map((value, index) => ({value, index})).sort((left, right) => left.value - right.value || left.index - right.index)
  const ranks = new Array<number>(values.length)
  const ties: number[] = []
  for (let start = 0; start < order.length;) {
    let end = start
    while (end + 1 < order.length && order[end + 1].value === order[start].value) end++
    const rank = (start + end) / 2 + 1
    for (let index = start; index <= end; index++) ranks[order[index].index] = rank
    if (end > start) ties.push(end - start + 1)
    start = end + 1
  }
  return {ranks, ties}
}

// Exact null distribution P(U = u) for samples of sizes m and n without ties.
function mannWhitneyDistribution(m: number, n: number): number[] {
  // previous[j] = distribution for (i − 1, j); built row by row over i.
  let previous: number[][] = Array.from({length: n + 1}, () => [1])
  for (let i = 1; i <= m; i++) {
    // (i, 0): U is always 0.
    const current: number[][] = [[1]]
    for (let j = 1; j <= n; j++) {
      const size = i * j + 1
      const values = new Array<number>(size).fill(0)
      const withoutI = previous[j]
      const withoutJ = current[j - 1]
      const weightI = i / (i + j)
      const weightJ = j / (i + j)
      // The largest value is from the first sample with probability i/(i+j): it exceeds all j others.
      for (let u = 0; u < withoutI.length; u++) values[u + j] += weightI * withoutI[u]
      for (let u = 0; u < withoutJ.length; u++) values[u] += weightJ * withoutJ[u]
      current.push(values)
    }
    previous = current
  }
  return previous[n]
}

export interface MannWhitney {u: number; pValue: number; method: 'exact' | 'normal'}

// Two-sided Mann–Whitney U test; U counts pairs where `group` exceeds `baseline` (ties ½),
// i.e. SciPy's mannwhitneyu(group, baseline).statistic.
export function mannWhitneyU(baseline: readonly number[], group: readonly number[]): MannWhitney | undefined {
  const n1 = group.length
  const n2 = baseline.length
  if (!n1 || !n2) return undefined
  const {ranks, ties} = midranks([...group, ...baseline])
  let rankSum = 0
  for (let index = 0; index < n1; index++) rankSum += ranks[index]
  const u = rankSum - n1 * (n1 + 1) / 2
  const product = n1 * n2
  if (!ties.length && n1 <= 50 && n2 <= 50) {
    const distribution = mannWhitneyDistribution(n1, n2)
    const extreme = Math.round(Math.min(u, product - u))
    let lower = 0
    for (let value = 0; value <= extreme; value++) lower += distribution[value]
    return {u, pValue: Math.min(1, 2 * lower), method: 'exact'}
  }
  const total = n1 + n2
  const tieTerm = ties.reduce((sum, size) => sum + size ** 3 - size, 0)
  const variance = product / 12 * (total + 1 - tieTerm / (total * (total - 1)))
  if (!(variance > 0)) return {u, pValue: 1, method: 'normal'}
  const z = (Math.abs(u - product / 2) - 0.5) / Math.sqrt(variance)
  return {u, pValue: Math.min(1, 2 * normalCDF(-Math.max(0, z))), method: 'normal'}
}

// Holm–Bonferroni step-down adjusted p-values. Undefined entries are not part of the family.
export function holmAdjust(pValues: ReadonlyArray<number | undefined>): Array<number | undefined> {
  const family = pValues.map((value, index) => ({value, index})).filter((item): item is {value: number; index: number} => item.value !== undefined && Number.isFinite(item.value))
  family.sort((left, right) => left.value - right.value || left.index - right.index)
  const adjusted: Array<number | undefined> = pValues.map(() => undefined)
  let running = 0
  family.forEach((item, rank) => {
    running = Math.max(running, Math.min(1, (family.length - rank) * item.value))
    adjusted[item.index] = running
  })
  return adjusted
}

// Small, fast, seedable 32-bit PRNG (mulberry32); deterministic across platforms.
export function mulberry32(seed: number): () => number {
  let state = seed >>> 0
  return () => {
    state = (state + 0x6d2b79f5) >>> 0
    let value = state
    value = Math.imul(value ^ (value >>> 15), value | 1)
    value ^= value + Math.imul(value ^ (value >>> 7), value | 61)
    return ((value ^ (value >>> 14)) >>> 0) / 4294967296
  }
}

export interface BootstrapInterval {estimate: number; low: number; high: number; iterations: number; seed: number}

// Percentile bootstrap CI of the median with a fixed seed (reproducible). Needs ≥ 2 values.
export function bootstrapMedianCI(values: readonly number[], {iterations = 2000, confidence = 0.95, seed = 20261119}: {iterations?: number; confidence?: number; seed?: number} = {}): BootstrapInterval | undefined {
  const sample = values.filter(Number.isFinite)
  if (sample.length < 2) return undefined
  const random = mulberry32(seed)
  const medians = new Array<number>(iterations)
  const resample = new Array<number>(sample.length)
  for (let iteration = 0; iteration < iterations; iteration++) {
    for (let index = 0; index < sample.length; index++) resample[index] = sample[Math.floor(random() * sample.length)]
    resample.sort((left, right) => left - right)
    medians[iteration] = quantileSorted(resample, 0.5)
  }
  medians.sort((left, right) => left - right)
  const alpha = (1 - confidence) / 2
  return {estimate: quantileSorted([...sample].sort((left, right) => left - right), 0.5), low: quantileSorted(medians, alpha), high: quantileSorted(medians, 1 - alpha), iterations, seed}
}

export interface SampleComparison {
  baselineCount: number
  groupCount: number
  baselineMean: number
  groupMean: number
  baselineStandardDeviation: number
  groupStandardDeviation: number
  // group − baseline; relative to |baseline mean| (undefined when the baseline mean is 0).
  difference: number
  relativeDifferencePercent?: number
  // Welch 95% CI of the difference, t statistic, df and two-sided p-value.
  differenceLow?: number
  differenceHigh?: number
  welchT?: number
  welchDegreesOfFreedom?: number
  pValue?: number
  // Holm-adjusted p-value; set by applyHolm over the caller's family of comparisons.
  adjustedPValue?: number
  cohensD?: number
  hedgesG?: number
  hedgesGLow?: number
  hedgesGHigh?: number
  mannWhitneyU?: number
  mannWhitneyPValue?: number
  mannWhitneyMethod?: 'exact' | 'normal'
  // Fewer than 3 values in either group: inference is not meaningful.
  insufficient: boolean
  // A group with ≥ 2 values and zero variance (Welch/Hedges may be undefined).
  zeroVariance: boolean
}

export function compareSamples(baseline: readonly number[], group: readonly number[]): SampleComparison {
  const left = baseline.filter(Number.isFinite)
  const right = group.filter(Number.isFinite)
  const baselineMean = left.length ? mean(left) : NaN
  const groupMean = right.length ? mean(right) : NaN
  const difference = groupMean - baselineMean
  const result: SampleComparison = {
    baselineCount: left.length, groupCount: right.length, baselineMean, groupMean,
    baselineStandardDeviation: Math.sqrt(sampleVariance(left)), groupStandardDeviation: Math.sqrt(sampleVariance(right)),
    difference,
    insufficient: left.length < 3 || right.length < 3,
    zeroVariance: (left.length > 1 && sampleVariance(left) === 0) || (right.length > 1 && sampleVariance(right) === 0)
  }
  if (Number.isFinite(difference) && baselineMean !== 0) result.relativeDifferencePercent = 100 * difference / Math.abs(baselineMean)
  const welch = welchTTest(left, right)
  if (welch?.pValue !== undefined) Object.assign(result, {differenceLow: welch.low, differenceHigh: welch.high, welchT: welch.t, welchDegreesOfFreedom: welch.degreesOfFreedom, pValue: welch.pValue})
  const effect = hedgesG(left, right)
  if (effect) Object.assign(result, {cohensD: effect.cohensD, hedgesG: effect.g, hedgesGLow: effect.low, hedgesGHigh: effect.high})
  const rank = mannWhitneyU(left, right)
  if (rank) Object.assign(result, {mannWhitneyU: rank.u, mannWhitneyPValue: rank.pValue, mannWhitneyMethod: rank.method})
  return result
}

export interface SampleGroup {id: string; label: string; values: readonly number[]}
export type ComparisonMode = 'baseline' | 'all-pairs'
export interface GroupComparison extends SampleComparison {baselineId: string; baseline: string; groupId: string; group: string}

// 'baseline': every other group vs the baseline (default: the first group).
// 'all-pairs': every pair (i < j) in group order, with the earlier group as reference.
// Holm adjustment is NOT applied here: apply applyHolm over the whole family.
export function compareGroups(groups: readonly SampleGroup[], {baseline, mode = 'baseline'}: {baseline?: string; mode?: ComparisonMode} = {}): GroupComparison[] {
  const pairs: Array<[SampleGroup, SampleGroup]> = []
  if (mode === 'all-pairs') {
    for (let i = 0; i < groups.length; i++) for (let j = i + 1; j < groups.length; j++) pairs.push([groups[i], groups[j]])
  } else {
    const reference = groups.find(group => group.id === baseline) ?? groups[0]
    if (reference) for (const group of groups) if (group !== reference) pairs.push([reference, group])
  }
  return pairs.map(([reference, group]) => ({baselineId: reference.id, baseline: reference.label, groupId: group.id, group: group.label, ...compareSamples(reference.values, group.values)}))
}

// Sets adjustedPValue (Holm) over all rows passed together; rows without a p-value stay undefined.
export function applyHolm<T extends {pValue?: number; adjustedPValue?: number}>(rows: readonly T[]): T[] {
  const adjusted = holmAdjust(rows.map(row => row.pValue))
  return rows.map((row, index) => adjusted[index] === undefined ? {...row} : {...row, adjustedPValue: adjusted[index]})
}

// Deterministic natural ordering (code points, digit runs compared numerically).
// Independent of the runtime locale, unlike String.prototype.localeCompare.
export function naturalCompare(left: string, right: string): number {
  const pattern = /(\d+)|(\D+)/g
  const a = left.match(pattern) ?? []
  const b = right.match(pattern) ?? []
  for (let index = 0; index < Math.min(a.length, b.length); index++) {
    const x = a[index]
    const y = b[index]
    if (x === y) continue
    const digits = /^\d/.test(x) && /^\d/.test(y)
    if (digits) {
      const difference = Number(x) - Number(y)
      if (difference) return difference < 0 ? -1 : 1
    }
    return x < y ? -1 : 1
  }
  return a.length - b.length || (left < right ? -1 : left > right ? 1 : 0)
}
