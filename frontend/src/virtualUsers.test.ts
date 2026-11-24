import {describe, expect, it} from 'vitest'
import {mixOverrides, parseLocustfile} from './locustParser'
import {userPacing} from './virtualUsers'
import {onlineBoutiqueLocustfile} from './test/locustFixtures'

const shopper = (wait: string) => `from locust import HttpUser, task, between, constant, constant_pacing, constant_throughput

class Shopper(HttpUser):
    wait_time = ${wait}

    @task
    def browse(self):
        self.client.get("/")
        self.client.get("/cart")
`

describe('Plain Locust users equivalent to a request rate', () => {
  it('uses the mean wait and the requests per task of the Online Boutique mix', () => {
    const pacing = userPacing(parseLocustfile(onlineBoutiqueLocustfile))!
    // 23 requests per 19 task executions, 5.5 s mean wait after each.
    expect(pacing.requestsPerTask).toBeCloseTo(23 / 19)
    expect(pacing.secondsPerRequest).toBeCloseTo(5.5 * 19 / 23)
    expect(pacing.waitTimes).toEqual(['between(1, 10)'])
  })

  it('follows the request mix', () => {
    const result = parseLocustfile(onlineBoutiqueLocustfile)
    const onlyCheckout = mixOverrides(result.users, {index: 0, setCurrency: 0, browseProduct: 0, addToCart: 0, viewCart: 0, checkout: 100})
    expect(userPacing(result, onlyCheckout)!.secondsPerRequest).toBeCloseTo(5.5 / 3)
  })

  it('reads every wait function as the time one task execution takes', () => {
    expect(userPacing(parseLocustfile(shopper('constant(4)')))!.secondsPerRequest).toBeCloseTo(2)
    expect(userPacing(parseLocustfile(shopper('constant_pacing(6)')))!.secondsPerRequest).toBeCloseTo(3)
    expect(userPacing(parseLocustfile(shopper('constant_throughput(0.5)')))!.secondsPerRequest).toBeCloseTo(1)
  })

  it('is unknown without a literal, positive wait time', () => {
    expect(userPacing(parseLocustfile(shopper('constant(0)')))).toBeUndefined()
    expect(userPacing(parseLocustfile(shopper('custom_wait')))).toBeUndefined()
  })
})
