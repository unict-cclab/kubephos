import {describe, expect, it} from 'vitest'
import {evaluatePath, logicalLines, mixOverrides, mixRunnable, parseLocustfile, readWaitTime, splitTopLevel, taskShares} from './locustParser'
import {bookinfoLocustfile, onlineBoutiqueLocustfile, trainTicketLocustfile} from './test/locustFixtures'

const byName = (result: ReturnType<typeof parseLocustfile>) => Object.fromEntries(result.tasks.map(task => [task.name, task]))

describe('Online Boutique upstream load generator', () => {
  const result = parseLocustfile(onlineBoutiqueLocustfile)
  const tasks = byName(result)

  it('reads the TaskSet dictionary with module-level task functions', () => {
    expect(result.status).toBe('ok')
    expect(result.warnings).toEqual([])
    expect(result.tasks.map(task => [task.name, task.weight])).toEqual([['index', 1], ['setCurrency', 2], ['browseProduct', 10], ['addToCart', 2], ['viewCart', 3], ['checkout', 1]])
    expect(result.users).toEqual([{name: 'WebsiteUser', weight: 1, tasks: [{name: 'UserBehavior', weight: 1, kind: 'taskset', children: expect.any(Array)}]}])
    expect(tasks.browseProduct.parents).toEqual(['WebsiteUser', 'UserBehavior'])
  })

  it('computes default shares through the nested TaskSet', () => {
    expect(tasks.browseProduct.share).toBeCloseTo(10 / 19)
    expect(tasks.checkout.share).toBeCloseTo(1 / 19)
    expect(result.tasks.reduce((sum, task) => sum + task.share, 0)).toBeCloseTo(1)
  })

  it('follows helper functions (checkout calls addToCart) and keeps literal path prefixes', () => {
    expect(tasks.index.calls).toEqual([{method: 'GET', path: '/'}])
    expect(tasks.setCurrency.calls).toEqual([{method: 'POST', path: '/setCurrency'}])
    expect(tasks.browseProduct.calls).toEqual([{method: 'GET', path: '/product/', dynamic: true}])
    expect(tasks.addToCart.calls).toEqual([{method: 'GET', path: '/product/', dynamic: true}, {method: 'POST', path: '/cart'}])
    expect(tasks.checkout.calls).toEqual([{method: 'GET', path: '/product/', dynamic: true}, {method: 'POST', path: '/cart'}, {method: 'POST', path: '/cart/checkout'}])
  })

  it('ignores functions that are not tasks (on_start, logout, empty_cart)', () => {
    expect(tasks.logout).toBeUndefined()
    expect(tasks.empty_cart).toBeUndefined()
    expect(tasks.on_start).toBeUndefined()
  })

  it('reads the wait time of the User class', () => {
    expect(result.waitTimes).toEqual({WebsiteUser: {kind: 'between', args: [1, 10], text: 'between(1, 10)'}})
  })
})

describe('Locust wait times', () => {
  it('reads literal wait functions, qualified or with keyword arguments', () => {
    expect(readWaitTime('constant(2)')).toEqual({kind: 'constant', args: [2], text: 'constant(2)'})
    expect(readWaitTime('locust.constant_pacing(5)')?.text).toBe('constant_pacing(5)')
    expect(readWaitTime('between(min_wait=0.5, max_wait=3)')?.args).toEqual([0.5, 3])
  })

  it('leaves computed or custom waits unknown', () => {
    expect(readWaitTime('between(1, MAX_WAIT)')).toBeUndefined()
    expect(readWaitTime('my_wait()')).toBeUndefined()
    expect(readWaitTime('constant(1, 2)')).toBeUndefined()
    expect(readWaitTime(undefined)).toBeUndefined()
  })

  it('inherits the wait time of a local base class', () => {
    const source = 'from locust import HttpUser, task, constant\n\nclass Base(HttpUser):\n    abstract = True\n    wait_time = constant(3)\n\nclass Shopper(Base):\n    @task\n    def home(self):\n        self.client.get("/")\n'
    expect(parseLocustfile(source).waitTimes).toEqual({Shopper: {kind: 'constant', args: [3], text: 'constant(3)'}})
  })
})

describe('catalog scripts', () => {
  it('reads the Bookinfo @task method', () => {
    const result = parseLocustfile(bookinfoLocustfile)
    expect(result.status).toBe('ok')
    expect(result.tasks).toEqual([{name: 'browse_product', weight: 1, share: 1, parents: ['WebsiteUser'], calls: [{method: 'GET', path: '/productpage'}]}])
  })

  it('reads Train-Ticket @task(n) methods and resolves paths passed to a helper method', () => {
    const result = parseLocustfile(trainTicketLocustfile)
    const tasks = byName(result)
    expect(result.status).toBe('ok')
    expect(result.tasks.map(task => [task.name, task.weight])).toEqual([['high_speed_search', 3], ['regular_search', 2]])
    expect(tasks.high_speed_search.share).toBeCloseTo(.6)
    expect(tasks.high_speed_search.calls).toEqual([{method: 'POST', path: '/api/v1/travelservice/trips/left', name: 'high-speed search'}])
    expect(tasks.regular_search.calls).toEqual([{method: 'POST', path: '/api/v1/travel2service/trips/left', name: 'regular search'}])
    // The login in on_start is not a task.
    expect(result.tasks.flatMap(task => task.calls).some(call => call.path.includes('login'))).toBe(false)
  })
})

describe('other Locust styles', () => {
  it('reads list tasks, tuples, weights on users, nested TaskSets and client.request', () => {
    const result = parseLocustfile(`
from locust import HttpUser, TaskSet, task, between
import locust

BASE = "/api"

def health(user):
    user.client.request("HEAD", "/healthz")

class Admin(TaskSet):
    @locust.task(weight=4)
    def report(self):
        self.client.get(f"/reports/{self.id}?full=1", name="/reports/[id]")

    @task
    class Deep(TaskSet):
        @task
        def deep(self):
            self.client.delete("/deep")

    @task
    def stop(self):
        self.interrupt()

class Shopper(HttpUser):
    weight = 3
    wait_time = between(1, 2)
    tasks = [health, (Admin, 2)]

    @task(2)
    def search(self):
        self._get("/search?q=%s" % "x")

    def _get(self, url):
        self.client.get(url)

class Anonymous(HttpUser):
    weight = 1
    tasks = {health: 1}
`)
    const tasks = byName(result)
    expect(result.users.map(user => [user.name, user.weight])).toEqual([['Shopper', 3], ['Anonymous', 1]])
    expect(tasks.health.calls).toEqual([{method: 'HEAD', path: '/healthz'}])
    expect(tasks.report).toMatchObject({weight: 4, calls: [{method: 'GET', path: '/reports/', dynamic: true, name: '/reports/[id]'}]})
    expect(tasks.deep.calls).toEqual([{method: 'DELETE', path: '/deep'}])
    expect(tasks.search.calls).toEqual([{method: 'GET', path: '/search?q=', dynamic: true}])
    expect(result.warnings).toContain('No HTTP call was found in task "stop".')
    expect(result.status).toBe('partial')
    // Shopper: health 1, Admin 2, search 2 → health 1/5 of 3/4 plus all of Anonymous (1/4).
    expect(tasks.health.share).toBeCloseTo(3 / 4 / 5 + 1 / 4)
    expect(tasks.search.share).toBeCloseTo(3 / 4 * 2 / 5)
    expect(result.tasks.reduce((sum, task) => sum + task.share, 0)).toBeCloseTo(1)
  })

  it('applies overrides by task and TaskSet name', () => {
    const {users} = parseLocustfile(onlineBoutiqueLocustfile)
    const shares = taskShares(users, {browseProduct: 0, checkout: 9})
    expect(shares.get('browseProduct')).toBe(0)
    expect(shares.get('checkout')).toBeCloseTo(9 / 17)
    expect([...taskShares(users, {UserBehavior: 0}).values()]).toEqual([])
  })

  it('turns percentages of all executions into per-class weights for nested TaskSets', () => {
    // Locust weighs siblings inside each class: the editor's percentages must keep their
    // meaning when a TaskSet has siblings (regression: the defaults showed checkout at 89%).
    const {users, tasks} = parseLocustfile(`
from locust import HttpUser, TaskSet, task

class Browse(TaskSet):
    @task(1)
    def a(self):
        self.client.get("/a")
    @task(1)
    def b(self):
        self.client.get("/b")

class WebsiteUser(HttpUser):
    tasks = {Browse: 3}
    @task(1)
    def checkout(self):
        self.client.post("/checkout")
`)
    const defaults = Object.fromEntries(tasks.map(task => [task.name, task.share * 100]))
    expect(defaults).toEqual({a: 37.5, b: 37.5, checkout: 25})
    const overrides = mixOverrides(users, defaults)
    expect(overrides).toEqual({Browse: 75, a: 37.5, b: 37.5, checkout: 25})
    expect(Object.fromEntries(taskShares(users, overrides))).toEqual({a: .375, b: .375, checkout: .25})
    const edited = mixOverrides(users, {a: 50, b: 0, checkout: 50})
    expect(Object.fromEntries(taskShares(users, edited))).toEqual({a: .5, b: 0, checkout: .5})
    // A TaskSet left without positive tasks weighs 0 instead of stopping the generator.
    expect(mixOverrides(users, {a: 0, b: 0, checkout: 1}).Browse).toBe(0)
    expect(mixRunnable(users, mixOverrides(users, {a: 0, b: 0, checkout: 1}))).toBe(true)
    expect(mixRunnable(users, mixOverrides(users, {a: 0, b: 0, checkout: 0}))).toBe(false)
    // The only TaskSet of a class needs no weight (Online Boutique's UserBehavior).
    const boutique = parseLocustfile(onlineBoutiqueLocustfile)
    expect(mixOverrides(boutique.users, {index: 1})).not.toHaveProperty('UserBehavior')
  })

  it('needs a positive task in every User class', () => {
    const {users} = parseLocustfile(`
class Shopper(HttpUser):
    @task
    def buy(self):
        self.client.get("/buy")

class Visitor(HttpUser):
    @task
    def look(self):
        self.client.get("/look")
`)
    expect(mixRunnable(users, {buy: 1, look: 1})).toBe(true)
    expect(mixRunnable(users, {buy: 1, look: 0})).toBe(false)
  })

  it('reports failures instead of throwing', () => {
    expect(parseLocustfile('import os\nprint("hello")')).toMatchObject({status: 'failed', tasks: [], warnings: ['No Locust User class was found in this file.']})
    expect(parseLocustfile('class A(HttpUser):\n    tasks = make_tasks()\n')).toMatchObject({status: 'failed'})
    expect(parseLocustfile('class A(HttpUser):\n    tasks = [missing]\n')).toMatchObject({status: 'partial', tasks: [{name: 'missing', calls: []}]})
    expect(parseLocustfile('')).toMatchObject({status: 'failed'})
  })

  it('tokenizes comments, strings and continued lines', () => {
    expect(logicalLines('a = (1,\n  2)  # c\nb = "#not a comment"\nc = """x\ny"""\nd = 1 + \\\n 2')).toEqual([
      {indent: 0, text: 'a = (1,   2)'}, {indent: 0, text: 'b = "#not a comment"'}, {indent: 0, text: 'c = """x y"""'}, {indent: 0, text: 'd = 1 +   2'}
    ])
    expect(splitTopLevel('a, f(b, c), "d,e"', ',')).toEqual(['a', 'f(b, c)', '"d,e"'])
    expect(evaluatePath('"/a/{}".format(x)')).toEqual({path: '/a/', dynamic: true})
    expect(evaluatePath('prefix + "/x"', {prefix: {path: '/api', dynamic: false}})).toEqual({path: '/api/x', dynamic: true})
    expect(evaluatePath('unknown')).toBeUndefined()
  })
})
