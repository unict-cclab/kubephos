package harbor

// Use the newly provisioned identity for the startup probe as well as the
// subsequent management API calls. Authentication failures are not transient.
func waitForRegistryHealthCommand(address, password string) string {
	request := "curl --connect-timeout 3 --max-time 10 --cacert " + tlsPath + "/ca.crt -sS -u " + shellQuote("admin:"+password) + " -o /tmp/kubephos-harbor-health.json -w '%{http_code}' " + shellQuote("https://"+address+"/api/v2.0/health")
	return "{ health_attempt=0; while :; do health_status=$(" + request + ") || health_status=000; " +
		"case \"$health_status\" in 401|403) printf 'Harbor startup check GET /api/v2.0/health rejected the management identity (HTTP %s).\\n' \"$health_status\" >&2; rm -f /tmp/kubephos-harbor-health.json; exit 1 ;; esac; " +
		"if test \"$health_status\" = 200 && jq -e '.status == \"healthy\"' /tmp/kubephos-harbor-health.json >/dev/null; then rm -f /tmp/kubephos-harbor-health.json; break; fi; " +
		"health_attempt=$((health_attempt + 1)); if test \"$health_attempt\" -ge 61; then printf 'Harbor startup check GET /api/v2.0/health did not become healthy (HTTP %s).\\n' \"$health_status\" >&2; rm -f /tmp/kubephos-harbor-health.json; exit 1; fi; sleep 5; done; }"
}
