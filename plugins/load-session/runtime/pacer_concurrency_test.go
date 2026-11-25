package loadsession

import "testing"

// The production pacer is shared by every Locust user of a worker. This test runs
// it as a discrete-event simulation with many concurrent users: gevent.sleep
// yields back to the scheduler, which resumes the user at the requested time.
func TestPacerHoldsTheTargetRateWithConcurrentUsers(t *testing.T) {
	script := `import heapq, math, random
clock = [0.0]
class Yield(Exception):
    def __init__(self, seconds):
        self.seconds = seconds
class time:
    @staticmethod
    def monotonic():
        return clock[0]
class gevent:
    @staticmethod
    def sleep(seconds):
        raise Yield(seconds)
class NoLock:
    def __enter__(self):
        return self
    def __exit__(self, *args):
        return False
request_lock = NoLock()
next_request_at = 0.0
rate = [lambda elapsed: 0.0]
def profile_elapsed():
    return clock[0]
def worker_rps_at(elapsed):
    return rate[0](elapsed)
` + wrapperSection(t, "def wait_for_request_slot(", "original_http_request =") + `
def simulate(profile, users, latency, seconds):
    global next_request_at
    random_source = random.Random(7)
    rate[0] = profile
    next_request_at = 0.0
    queue = [(0.0, user) for user in range(users)]
    issued = []
    while queue:
        now, user = heapq.heappop(queue)
        if now > seconds:
            break
        clock[0] = now
        before = next_request_at
        try:
            wait_for_request_slot()
            issue = now
        except Yield as pause:
            if next_request_at == before:
                heapq.heappush(queue, (now + pause.seconds, user))
                continue
            issue = now + pause.seconds
        if issue <= seconds:
            issued.append(issue)
        heapq.heappush(queue, (issue + random_source.expovariate(1.0 / latency), user))
    return issued
def achieved(target, users, latency=0.02, seconds=200.0):
    return len(simulate(lambda elapsed: target, users, latency, seconds)) / seconds
# Locust starts ceil(rate) users; fractional rates used to overshoot up to 2x.
for target in (2.5, 9.5, 10.0, 33.34, 50.5, 100.0):
    value = achieved(target, math.ceil(target))
    assert abs(value - target) <= 0.02 * target + 0.02, (target, value)
assert abs(achieved(9.5, 10, latency=0.2) - 9.5) < 0.2
# Slow responses: rate x latency users are busy at once (Little's law). With
# only ceil(rate) users the schedule slips; the latency-sized users keep it.
assert achieved(10.0, 10, latency=2.0, seconds=400.0) < 6.5
assert abs(achieved(10.0, 30, latency=2.0, seconds=400.0) - 10.0) < 0.3
# Users left over after a downward step must not raise the rate.
assert abs(achieved(5.0, 100) - 5.0) < 0.1, achieved(5.0, 100)
# A sinusoid keeps its mean, and an upward step applies within the horizon.
wave = simulate(lambda elapsed: 50 + 40 * math.sin(2 * math.pi * elapsed / 120), 91, 0.02, 240.0)
assert abs(len(wave) / 240.0 - 50.0) < 0.5, len(wave) / 240.0
step = simulate(lambda elapsed: 5.0 if elapsed < 100 else 50.0, 100, 0.02, 200.0)
after = sum(1 for issue in step if 102 <= issue < 200) / 98.0
assert abs(after - 50.0) < 0.5, after
# Coordinated-omission evidence: lag stays ~0 while enough users keep the schedule...
def lagged(target, users, latency, seconds=200.0):
    for key in schedule_stats:
        schedule_stats[key] = 0
    offered = len(simulate(lambda elapsed: target, users, latency, seconds)) / seconds
    return offered, schedule_stats["lag_sum"] / max(1, schedule_stats["count"]), schedule_stats["late"], schedule_stats["count"]
offered, mean_lag, late, count = lagged(10.0, 10, 0.02)
assert abs(offered - 10.0) < 0.2 and mean_lag < 0.002 and late <= 0.002 * count, (offered, mean_lag, late, count)
offered, mean_lag, late, count = lagged(50.0, 100, 0.02)
assert abs(offered - 50.0) < 0.5 and mean_lag < 0.002 and late == 0, (offered, mean_lag, late, count)
# ...and grows when the users are saturated: 1 user, 0.5 s latency, 10 RPS target.
offered, mean_lag, late, count = lagged(10.0, 1, 0.5)
assert offered < 3.0 and mean_lag > 0.3 and late > 0.6 * count and schedule_stats["max_lag"] > 1.0, (offered, mean_lag, late, count)
`
	runPython(t, script)
}
