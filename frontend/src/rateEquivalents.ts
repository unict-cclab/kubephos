import {requestsPerExecution, taskShares, type LocustParseResult, type LocustWaitTime} from './locustParser'

// What a Locust request rate means elsewhere, with the scenario's request mix.
//
// Virtual users: plain (closed-loop) Locust users that send the same rate. Such a
// user runs one task, then waits `wait_time`, so per task execution it sends the
// task's requests and spends the mean wait plus their response times. Response
// time is left out, so the figure is a lower bound (each user also waits for its
// responses: rate x response time more users). KubePhos paces the rate itself.
//
// Server-side rate: Locust counts a request whose redirect it follows once, while
// the application's entry point receives both. The endpoint's declared redirects
// give the extra requests of each task.

export interface Redirect {method: string; path: string}

export interface RateEquivalents {
  // Seconds one plain Locust user takes per request; absent without a literal, positive wait time.
  secondsPerRequest?: number
  // Requests the entry point receives per Locust request; absent when the endpoint declares no redirect.
  serverRequestsPerRequest?: number
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

export function rateEquivalents(result: LocustParseResult, overrides: Record<string, number> = {}, redirects: Redirect[] = []): RateEquivalents {
  const tasks = new Map(result.tasks.map(task => [task.name, task]))
  const requestsOf = (shares: Map<string, number>) => [...shares].reduce((sum, [name, share]) => sum + share * (tasks.has(name) ? requestsPerExecution(tasks.get(name)!) : 0), 0)
  const equivalents: RateEquivalents = {}
  const users = result.users.filter(user => user.weight > 0)
  const totalWeight = users.reduce((sum, user) => sum + user.weight, 0)
  let requestsPerSecond = 0
  for (const user of users) {
    const wait = result.waitTimes?.[user.name]
    const seconds = wait ? waitSeconds(wait) : 0
    const requests = requestsOf(taskShares([user], overrides))
    if (!(seconds > 0) || !(requests > 0)) { requestsPerSecond = 0; break }
    requestsPerSecond += user.weight / totalWeight * requests / seconds
  }
  if (requestsPerSecond > 0) equivalents.secondsPerRequest = 1 / requestsPerSecond
  const redirected = new Set(redirects.map(redirect => `${redirect.method.toUpperCase()} ${redirect.path}`))
  if (redirected.size) {
    const shares = taskShares(result.users, overrides)
    const client = requestsOf(shares)
    const extra = [...shares].reduce((sum, [name, share]) => sum + share * (tasks.get(name)?.calls.filter(call => !call.dynamic && redirected.has(`${call.method} ${call.path}`)).length ?? 0), 0)
    if (client > 0) equivalents.serverRequestsPerRequest = (client + extra) / client
  }
  return equivalents
}
