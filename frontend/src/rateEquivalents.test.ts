import {describe, expect, it} from 'vitest'
import {mixOverrides, parseLocustfile} from './locustParser'
import {rateEquivalents} from './rateEquivalents'
import {onlineBoutiqueLocustfile} from './test/locustFixtures'

const shopper = (wait: string) => `from locust import HttpUser, task, between, constant, constant_pacing, constant_throughput

class Shopper(HttpUser):
    wait_time = ${wait}

    @task
    def browse(self):
        self.client.get("/")
        self.client.get("/cart")
`

const onlineBoutiqueRedirects = [{method: 'POST', path: '/setCurrency'}, {method: 'POST', path: '/cart'}, {method: 'POST', path: '/cart/empty'}, {method: 'GET', path: '/logout'}]

describe('Plain Locust users equivalent to a request rate', () => {
  it('uses the mean wait and the requests per task of the Online Boutique mix', () => {
    // 23 requests per 19 task executions, 5.5 s mean wait after each.
    expect(rateEquivalents(parseLocustfile(onlineBoutiqueLocustfile)).secondsPerRequest).toBeCloseTo(5.5 * 19 / 23)
  })

  it('follows the request mix', () => {
    const result = parseLocustfile(onlineBoutiqueLocustfile)
    const onlyCheckout = mixOverrides(result.users, {index: 0, setCurrency: 0, browseProduct: 0, addToCart: 0, viewCart: 0, checkout: 100})
    expect(rateEquivalents(result, onlyCheckout).secondsPerRequest).toBeCloseTo(5.5 / 3)
  })

  it('reads every wait function as the time one task execution takes', () => {
    expect(rateEquivalents(parseLocustfile(shopper('constant(4)'))).secondsPerRequest).toBeCloseTo(2)
    expect(rateEquivalents(parseLocustfile(shopper('constant_pacing(6)'))).secondsPerRequest).toBeCloseTo(3)
    expect(rateEquivalents(parseLocustfile(shopper('constant_throughput(0.5)'))).secondsPerRequest).toBeCloseTo(1)
  })

  it('is unknown without a literal, positive wait time', () => {
    expect(rateEquivalents(parseLocustfile(shopper('constant(0)'))).secondsPerRequest).toBeUndefined()
    expect(rateEquivalents(parseLocustfile(shopper('custom_wait'))).secondsPerRequest).toBeUndefined()
  })
})

describe('Server-side rate of a Locust rate', () => {
  it('adds the redirects the client follows (Online Boutique: 300 req/s reach the server as 365)', () => {
    // setCurrency (2/19), addToCart (2/19) and checkout (1/19) each post once to a
    // redirecting path: 23 + 5 requests per 19 task executions.
    const {serverRequestsPerRequest} = rateEquivalents(parseLocustfile(onlineBoutiqueLocustfile), {}, onlineBoutiqueRedirects)
    expect(serverRequestsPerRequest).toBeCloseTo(28 / 23)
    expect(Math.round(300 * serverRequestsPerRequest!)).toBe(365)
  })

  it('follows the request mix and ignores redirects of other methods', () => {
    const result = parseLocustfile(onlineBoutiqueLocustfile)
    const onlyBrowse = mixOverrides(result.users, {index: 0, setCurrency: 0, browseProduct: 100, addToCart: 0, viewCart: 0, checkout: 0})
    expect(rateEquivalents(result, onlyBrowse, onlineBoutiqueRedirects).serverRequestsPerRequest).toBe(1)
    expect(rateEquivalents(result, {}, [{method: 'GET', path: '/setCurrency'}]).serverRequestsPerRequest).toBe(1)
  })

  it('is unknown when the endpoint declares no redirect', () => {
    expect(rateEquivalents(parseLocustfile(onlineBoutiqueLocustfile)).serverRequestsPerRequest).toBeUndefined()
  })
})
