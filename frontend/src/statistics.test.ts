import {describe, expect, it} from 'vitest'
import {applyHolm, bootstrapMedianCI, compareGroups, compareSamples, effectiveSampleSize, hedgesCorrection, hedgesG, holmAdjust, lag1Autocorrelation, mannWhitneyU, mulberry32, naturalCompare, normalCDF, normalQuantile, regularizedIncompleteBeta, studentTCDF, studentTQuantile, studentTwoSidedPValue, summarize, summarizeTimeSeries, tCritical, welchTTest} from './statistics'

// Reference values: SciPy 1.18.1 (scipy.stats.t.ppf/cdf, scipy.stats.norm, scipy.special.betainc,
// scipy.stats.ttest_ind(equal_var=False) with .confidence_interval(), scipy.stats.mannwhitneyu),
// computed in python:3.12-slim. They agree with R's qt/pt/t.test/wilcox.test to the digits shown.

describe('Student t quantile', () => {
  // stats.t.ppf(0.975, df)
  const references: Array<[number, number]> = [
    [1, 12.706204736174694], [2, 4.302652729749462], [3, 3.1824463052837078], [5, 2.5705818356363146],
    [10, 2.228138851986274], [29, 2.045229642132703], [30, 2.0422724563012378], [31, 2.039513446396408],
    [40, 2.021075390306273], [60, 2.0002978220142604], [120, 1.9799304050824402], [1000, 1.9623390808264083]
  ]
  it.each(references)('matches t_{0.975, %d} to 1e-9', (df, expected) => {
    expect(Math.abs(studentTQuantile(0.975, df) - expected)).toBeLessThan(1e-9)
    expect(Math.abs(tCritical(df) - expected)).toBeLessThan(1e-9)
  })

  it.each<[number, number, number]>([
    [0.995, 4, 4.604094871349992], [0.6, 3, 0.2766706623326898], [0.025, 7, -2.3646242515927844],
    [0.975, 7.5, 2.333039626864974], [0.9, 2.3, 1.7819239901184767], [0.999, 1, 318.30883898555015],
    [0.975, 0.5, 164.55767348048818]
  ])('matches qt(%f, %f) including non-integer df', (p, df, expected) => {
    expect(Math.abs(studentTQuantile(p, df) - expected) / Math.abs(expected)).toBeLessThan(1e-9)
  })

  it('is the inverse of the CDF and symmetric', () => {
    for (const df of [1.5, 4, 17.3, 250]) for (const p of [0.001, 0.05, 0.3, 0.7, 0.99]) {
      expect(studentTCDF(studentTQuantile(p, df), df)).toBeCloseTo(p, 12)
      expect(studentTQuantile(p, df)).toBeCloseTo(-studentTQuantile(1 - p, df), 10)
    }
    expect(studentTQuantile(0.5, 3)).toBe(0)
    expect(studentTQuantile(1, 3)).toBe(Infinity)
    expect(studentTQuantile(0.5, 0)).toBeNaN()
  })
})

describe('Student t CDF and special functions', () => {
  it.each<[number, number, number]>([
    [2.5, 4, 0.966616727594006], [-1.3, 12.3, 0.10871748560543157], [0, 5, 0.5],
    [8, 3, 0.9979617112061072], [-30, 2, 0.0005546313409798295], [1.96, 1e5, 0.9750007184028492]
  ])('pt(%f, %f)', (t, df, expected) => {
    // ln Γ of very large arguments limits the absolute accuracy to ~1e-11 at df = 1e5.
    expect(Math.abs(studentTCDF(t, df) - expected)).toBeLessThan(1e-10)
  })

  it('keeps precision for small two-sided p-values', () => {
    // 2 * stats.t.cdf(-30, 2)
    expect(studentTwoSidedPValue(30, 2) / (2 * 0.0005546313409798295) - 1).toBeLessThan(1e-10)
  })

  it('matches the normal distribution and the incomplete beta function', () => {
    expect(normalQuantile(0.975)).toBeCloseTo(1.959963984540054, 14)
    expect(normalQuantile(0.001)).toBeCloseTo(-3.090232306167813, 13)
    expect(normalCDF(-3)).toBeCloseTo(0.0013498980316300933, 15)
    expect(regularizedIncompleteBeta(0.3, 2.5, 0.5)).toBeCloseTo(0.018927124071945658, 14)
    expect(regularizedIncompleteBeta(0.8, 10, 3)).toBeCloseTo(0.5583457484800002, 14)
  })
})

describe('summaries', () => {
  it('use the exact t quantile for independent repetitions', () => {
    // 41 values -> df 40 (the old coarse table used 2.0 there).
    const values = Array.from({length: 41}, (_, index) => index)
    const summary = summarize(values)
    const margin = 2.021075390306273 * summary.standardDeviation / Math.sqrt(41)
    expect(summary.confidence95High - summary.mean).toBeCloseTo(margin, 10)
    expect(summary.nEffective).toBeUndefined()
    expect(summarize([3, 1, 2]).median).toBe(2)
  })

  it('widen the CI of an autocorrelated time series with the AR(1) effective sample size', () => {
    const series = [5, 7, 6, 9, 8, 10, 12, 11, 13, 12]
    // numpy: r1 = sum((x[:-1]-m)*(x[1:]-m)) / sum((x-m)^2), neff = n(1-r1)/(1+r1)
    expect(lag1Autocorrelation(series)).toBeCloseTo(0.5977973568281938, 14)
    expect(effectiveSampleSize(series).nEffective).toBeCloseTo(2.517231872070582, 12)
    const independent = summarize(series)
    const dependent = summarizeTimeSeries(series)
    expect(dependent.mean).toBe(independent.mean)
    expect(dependent.standardDeviation).toBe(independent.standardDeviation)
    expect(dependent.nEffective).toBeCloseTo(2.517231872070582, 12)
    expect(dependent.lag1Autocorrelation).toBeCloseTo(0.5977973568281938, 14)
    const margin = studentTQuantile(0.975, 2.517231872070582 - 1) * independent.standardDeviation / Math.sqrt(2.517231872070582)
    expect(dependent.confidence95High - dependent.mean).toBeCloseTo(margin, 10)
    expect(dependent.confidence95High - dependent.mean).toBeGreaterThan(3 * (independent.confidence95High - independent.mean))
  })

  it('clamp the effective sample size to [2, n]', () => {
    // Alternating series: negative autocorrelation would inflate n_eff above n.
    expect(effectiveSampleSize([1, 3, 1, 3, 1, 3]).nEffective).toBe(6)
    // A trend: rho close to 1 would push n_eff below 2.
    expect(effectiveSampleSize(Array.from({length: 50}, (_, index) => index)).nEffective).toBe(2)
    expect(effectiveSampleSize([4, 4, 4, 4])).toEqual({n: 4, nEffective: 4, rho: undefined})
    expect(summarizeTimeSeries([2])).toMatchObject({count: 1, confidence95Low: 2, confidence95High: 2, nEffective: 1})
  })

  it('ignore unavailable (non-finite) samples instead of counting them as zero', () => {
    expect(summarize([1, NaN, 3, Infinity]).count).toBe(2)
    expect(summarizeTimeSeries([1, NaN, 3]).mean).toBe(2)
  })
})

describe('Welch t-test', () => {
  const A = [12.1, 13.4, 11.8, 12.9, 13.0]
  const B = [14.2, 15.1, 13.8, 14.9]

  it('matches scipy.stats.ttest_ind(B, A, equal_var=False)', () => {
    const result = welchTTest(A, B)!
    expect(result.t).toBeCloseTo(4.380818193823761, 12)
    expect(result.degreesOfFreedom).toBeCloseTo(6.822010006357872, 12)
    expect(result.pValue).toBeCloseTo(0.003436467554137229, 13)
    expect(result.low).toBeCloseTo(0.8506975431304116, 10)
    expect(result.high).toBeCloseTo(2.8693024568695873, 10)
  })

  it('matches a non-significant reference', () => {
    const result = welchTTest([100, 102, 98, 101], [100.5, 103, 99, 101.5, 100])!
    expect(result.t).toBeCloseTo(0.5033038091992732, 12)
    expect(result.degreesOfFreedom).toBeCloseTo(6.165720193604952, 12)
    expect(result.pValue).toBeCloseTo(0.6322230022281816, 12)
    expect(result.low).toBeCloseTo(-2.1066096752215246, 10)
    expect(result.high).toBeCloseTo(3.206609675221519, 10)
  })

  it('is undefined for fewer than two values and degenerate for zero variance', () => {
    expect(welchTTest([1], [2, 3])).toBeUndefined()
    expect(welchTTest([1, 1], [2, 2])).toEqual({difference: 1, standardError: 0})
  })
})

describe("Hedges' g", () => {
  // d = (mean(b)-mean(a))/s_pooled; J = Γ(df/2)/(√(df/2)Γ((df−1)/2)) via scipy.special.gamma;
  // CI = g ± z_{0.975}·J·√((n1+n2)/(n1n2) + d²/(2(n1+n2))).
  it('uses the exact gamma correction and the Hedges–Olkin variance', () => {
    const result = hedgesG([12.1, 13.4, 11.8, 12.9, 13.0], [14.2, 15.1, 13.8, 14.9])!
    expect(result.cohensD).toBeCloseTo(2.903820363236999, 12)
    expect(result.correction).toBeCloseTo(0.8882029076727513, 12)
    expect(result.g).toBeCloseTo(2.5791816899864477, 12)
    expect(result.low).toBeCloseTo(0.9108246596597707, 10)
    expect(result.high).toBeCloseTo(4.247538720313125, 10)
    const small = hedgesG([100, 102, 98, 101], [100.5, 103, 99, 101.5, 100])!
    expect(small.g).toBeCloseTo(0.3042182732584387, 12)
    expect(small.low).toBeCloseTo(-0.8720027686287413, 10)
    expect(small.high).toBeCloseTo(1.4804393151456186, 10)
  })

  it('approaches the common approximation 1 − 3/(4df − 1) for large df', () => {
    expect(hedgesCorrection(100)).toBeCloseTo(1 - 3 / 399, 5)
    expect(hedgesG([1, 1], [2, 2])).toBeUndefined()
  })
})

describe('Mann–Whitney U', () => {
  it('is exact without ties (scipy method="exact")', () => {
    expect(mannWhitneyU([12.1, 13.4, 11.8, 12.9, 13.0], [14.2, 15.1, 13.8, 14.9])).toEqual({u: 20, pValue: expect.closeTo(0.015873015873015872, 14), method: 'exact'})
    expect(mannWhitneyU([4, 5, 6], [1, 2, 3])).toEqual({u: 0, pValue: expect.closeTo(0.1, 14), method: 'exact'})
    // Identical distributions in the centre: p clipped to 1.
    expect(mannWhitneyU([1, 4], [2, 3])?.pValue).toBe(1)
  })

  it('uses the tie- and continuity-corrected normal approximation with ties', () => {
    // mannwhitneyu([1,2,2,3,4], [2,3,5,6,6,7], method='asymptotic', use_continuity=True)
    expect(mannWhitneyU([2, 3, 5, 6, 6, 7], [1, 2, 2, 3, 4])).toEqual({u: 4.5, pValue: expect.closeTo(0.06414661873440437, 14), method: 'normal'})
    expect(mannWhitneyU([1, 1], [1, 1])).toEqual({u: 2, pValue: 1, method: 'normal'})
  })
})

describe('Holm–Bonferroni', () => {
  it('adjusts step-down with monotonicity and ignores undefined p-values', () => {
    // Holm (1979): sorted 0.01, 0.02, 0.03, 0.5 -> 0.04, 0.06, 0.06, 0.5.
    expect(holmAdjust([0.03, undefined, 0.01, 0.5, 0.02])).toEqual([0.06, undefined, 0.04, 0.5, 0.06])
    expect(holmAdjust([0.9, 0.8])).toEqual([1, 1])
    expect(holmAdjust([0.01, 0.04])).toEqual([0.02, 0.04])
    expect(holmAdjust([0.6, 0.7]).every(value => value! <= 1)).toBe(true)
    expect(applyHolm([{pValue: 0.04}, {pValue: 0.01}, {}])).toEqual([{pValue: 0.04, adjustedPValue: 0.04}, {pValue: 0.01, adjustedPValue: 0.02}, {}])
  })
})

describe('bootstrap', () => {
  it('is reproducible with a fixed seed and brackets the median', () => {
    const values = [3, 9, 4, 7, 5, 8, 6, 12, 2, 10]
    const first = bootstrapMedianCI(values)!
    expect(bootstrapMedianCI(values)).toEqual(first)
    expect(first.estimate).toBe(6.5)
    expect(first.low).toBeLessThanOrEqual(6.5)
    expect(first.high).toBeGreaterThanOrEqual(6.5)
    expect(first.low).toBeGreaterThanOrEqual(2)
    expect(first.high).toBeLessThanOrEqual(12)
    expect(bootstrapMedianCI(values, {seed: 7})).not.toEqual(first)
    expect(bootstrapMedianCI([1])).toBeUndefined()
  })

  it('uses a deterministic PRNG', () => {
    const left = mulberry32(42)
    const right = mulberry32(42)
    const draws = Array.from({length: 5}, () => left())
    expect(Array.from({length: 5}, () => right())).toEqual(draws)
    expect(draws.every(value => value >= 0 && value < 1)).toBe(true)
  })
})

describe('group comparisons', () => {
  const groups = [
    {id: 'a', label: 'A', values: [12.1, 13.4, 11.8, 12.9, 13.0]},
    {id: 'b', label: 'B', values: [14.2, 15.1, 13.8, 14.9]},
    {id: 'c', label: 'C', values: [12, 12]}
  ]

  it('compares every group with the baseline (default: first)', () => {
    const rows = compareGroups(groups)
    expect(rows.map(row => `${row.baseline}->${row.group}`)).toEqual(['A->B', 'A->C'])
    expect(rows[0]).toMatchObject({baselineCount: 5, groupCount: 4, insufficient: false, zeroVariance: false, pValue: expect.closeTo(0.003436467554137229, 13), hedgesG: expect.closeTo(2.5791816899864477, 12), mannWhitneyPValue: expect.closeTo(0.015873015873015872, 14)})
    expect(rows[0].relativeDifferencePercent).toBeCloseTo(100 * rows[0].difference / 12.64, 12)
    expect(rows[1]).toMatchObject({insufficient: true, zeroVariance: true})
    expect(compareGroups(groups, {baseline: 'b'}).map(row => `${row.baseline}->${row.group}`)).toEqual(['B->A', 'B->C'])
  })

  it('supports all pairs', () => {
    expect(compareGroups(groups, {mode: 'all-pairs'}).map(row => `${row.baselineId}->${row.groupId}`)).toEqual(['a->b', 'a->c', 'b->c'])
  })

  it('reports no relative difference against a zero baseline', () => {
    expect(compareSamples([0, 0, 0], [1, 2, 3]).relativeDifferencePercent).toBeUndefined()
  })
})

it('orders names naturally without depending on the locale', () => {
  expect(['b10', 'B2', 'b2', 'a', 'b1'].sort(naturalCompare)).toEqual(['B2', 'a', 'b1', 'b2', 'b10'])
})
