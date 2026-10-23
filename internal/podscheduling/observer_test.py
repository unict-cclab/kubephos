import copy
import json
import os
import unittest

os.environ["WORKLOAD_TARGETS"] = json.dumps([{"name": "api", "selector": {"app": "api"}}])
os.environ["POD_NAMESPACE"] = "run"
import observer


class ObservationTest(unittest.TestCase):
    def setUp(self):
        observer.records.clear()

    def pod(self, uid="first", app="api", scheduled=True):
        return {"metadata": {"uid": uid, "name": "api-0", "labels": {"app": app}, "creationTimestamp": "2026-10-20T10:00:00Z"}, "status": {"conditions": [{"type": "PodScheduled", "status": "True" if scheduled else "False", "lastTransitionTime": "2026-10-20T10:00:02Z"}]}}

    def test_only_application_pods_and_completed_scheduling(self):
        observer.observe(self.pod(app="load-generator"))
        observer.observe(self.pod(scheduled=False))
        self.assertEqual(observer.records, {})
        observer.observe(self.pod())
        self.assertEqual(observer.records["first"]["latency"], 2000)

    def test_duplicate_watch_events_and_replacement_uids(self):
        original = self.pod()
        observer.observe(original)
        observer.observe(copy.deepcopy(original))
        observer.observe(self.pod(uid="replacement"))
        self.assertEqual(len(observer.records), 2)
        self.assertEqual(observer.records["first"]["pod"], observer.records["replacement"]["pod"])

    def test_subsecond_timestamps_are_not_invented(self):
        pod = self.pod()
        pod["status"]["conditions"][0]["lastTransitionTime"] = pod["metadata"]["creationTimestamp"]
        observer.observe(pod)
        self.assertEqual(observer.records["first"]["latency"], 0)


if __name__ == "__main__":
    unittest.main()
