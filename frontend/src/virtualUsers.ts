import {requestsPerExecution, taskShares, type LocustParseResult, type LocustWaitTime} from './locustParser'

// Virtual users of plain (closed-loop) Locust that send a given request rate.
//
// A plain Locust user runs one task, then waits `wait_time`: per task execution
// it sends the task's requests and spends the mean wait plus their response
// times. KubePhos paces the configured rate itself (open loop, no wait), so this
// is a reference for comparing with plain Locust runs, not a generator setting.
// Response time is left out: each user also spends rate x latency seconds per
// second waiting for responses, so the figure is a lower bound.

export interface UserPacing {
  // Seconds one plain Locust user takes per request, response time excluded.
  secondsPerRequest: number
  // Mean requests one task execution sends, with the current request mix.
  requestsPerTask: number
  // The wait times of the User classes, as written (e.g. "between(1, 10)").
  waitTimes: string[]
}

// Mean seconds one task execution adds because of wait_time.
function waitSeconds(wait: LocustWaitTime): number {
  switch (wait.kind) {
    case 'between': return (wait.args[0] + wait.args[1]) / 2
    // constant_pacing(p) makes every task execution last p seconds in total.
    case 'constant':
    case 'constant_pacing': return wait.args[0]
    case 'constant_throughput': return wait.args[0] > 0 ? 1 / wait.args[0] : 0
  }
}

// Undefined when a User class has no literal wait time, no wait at all (its rate
// then depends only on latency) or no requests.
export function userPacing(result: LocustParseResult, overrides: Record<string, number> = {}): UserPacing | undefined {
  const users = result.users.filter(user => user.weight > 0)
  const totalWeight = users.reduce((sum, user) => sum + user.weight, 0)
  const tasks = new Map(result.tasks.map(task => [task.name, task]))
  let requestsPerSecond = 0
  let requestsPerTask = 0
  const waitTimes = new Set<string>()
  for (const user of users) {
    const wait = result.waitTimes?.[user.name]
    const seconds = wait ? waitSeconds(wait) : 0
    if (!wait || !(seconds > 0)) return undefined
    let requests = 0
    for (const [name, share] of taskShares([user], overrides)) {
      const task = tasks.get(name)
      if (task) requests += share * requestsPerExecution(task)
    }
    if (!(requests > 0)) return undefined
    const fraction = user.weight / totalWeight
    requestsPerSecond += fraction * requests / seconds
    requestsPerTask += fraction * requests
    waitTimes.add(wait.text)
  }
  if (!(requestsPerSecond > 0)) return undefined
  return {secondsPerRequest: 1 / requestsPerSecond, requestsPerTask, waitTimes: [...waitTimes]}
}
