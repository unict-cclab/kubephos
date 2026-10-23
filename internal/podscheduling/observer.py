import datetime
import http.server
import json
import os
import ssl
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

TARGETS = json.loads(os.environ["WORKLOAD_TARGETS"])
NAMESPACE = os.environ["POD_NAMESPACE"]
ROOT = "/var/run/secrets/kubernetes.io/serviceaccount/"
URL = "https://kubernetes.default.svc/api/v1/namespaces/" + urllib.parse.quote(NAMESPACE, safe="") + "/pods"
TLS = None
records = {}
lock = threading.Lock()
ready = threading.Event()


def request(url):
    global TLS
    if TLS is None:
        TLS = ssl.create_default_context(cafile=ROOT + "ca.crt")
    with open(ROOT + "token") as source:
        token = source.read().strip()
    return urllib.request.urlopen(urllib.request.Request(url, headers={"Authorization": "Bearer " + token}), context=TLS, timeout=45)


def timestamp(value):
    return datetime.datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()


def observe(pod):
    meta = pod.get("metadata", {})
    labels = meta.get("labels", {})
    workload = next((target["name"] for target in TARGETS if target["selector"] and all(labels.get(key) == value for key, value in target["selector"].items())), None)
    uid = meta.get("uid")
    if not workload or not uid:
        return
    condition = next((item for item in pod.get("status", {}).get("conditions", []) if item.get("type") == "PodScheduled" and item.get("status") == "True"), None)
    if not condition or not meta.get("creationTimestamp") or not condition.get("lastTransitionTime"):
        return
    created, scheduled = timestamp(meta["creationTimestamp"]), timestamp(condition["lastTransitionTime"])
    if scheduled < created:
        return
    with lock:
        records.setdefault(uid, {"pod": meta["name"], "workload": workload, "created": created, "scheduled": scheduled, "latency": 1000 * (scheduled - created)})


def watch():
    version = None
    while True:
        try:
            if version is None:
                with request(URL) as response:
                    listing = json.load(response)
                for pod in listing.get("items", []):
                    observe(pod)
                version = listing["metadata"]["resourceVersion"]
                ready.set()
            query = urllib.parse.urlencode({"watch": "true", "allowWatchBookmarks": "true", "timeoutSeconds": 30, "resourceVersion": version})
            with request(URL + "?" + query) as response:
                for line in response:
                    if not line.strip():
                        continue
                    event = json.loads(line)
                    obj = event.get("object", {})
                    if event.get("type") == "ERROR":
                        version = None
                        break
                    version = obj.get("metadata", {}).get("resourceVersion", version)
                    if event.get("type") in ("ADDED", "MODIFIED", "DELETED"):
                        observe(obj)
        except (OSError, ValueError, KeyError):
            version = None
            time.sleep(1)


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        if self.path == "/healthz":
            self.send_response(200 if ready.is_set() else 503)
            self.end_headers()
            return
        if self.path != "/stats/prometheus":
            self.send_response(404)
            self.end_headers()
            return
        with lock:
            snapshot = list(records.items())
        lines = ["# TYPE kubephos_pod_scheduling_observed_pods gauge", "kubephos_pod_scheduling_observed_pods %d" % len(snapshot)]
        for uid, record in sorted(snapshot):
            labels = ",".join(key + "=" + json.dumps(value) for key, value in {"pod_uid": uid, "workload_pod": record["pod"], "workload": record["workload"]}.items())
            for metric, field in (("latency_milliseconds", "latency"), ("scheduled_timestamp_seconds", "scheduled")):
                lines.append("kubephos_pod_scheduling_%s{%s} %.6f" % (metric, labels, record[field]))
        body = ("\n".join(lines) + "\n").encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/plain; version=0.0.4")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


if __name__ == "__main__":
    threading.Thread(target=watch, daemon=True).start()
    http.server.ThreadingHTTPServer(("0.0.0.0", 9647), Handler).serve_forever()
