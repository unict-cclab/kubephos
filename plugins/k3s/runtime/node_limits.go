package k3s

// NodeWatchLimitsCommand prepares filesystem watchers for the managed agents.
// It is also safe to run on an existing node; larger operator limits are kept.
func NodeWatchLimitsCommand() string {
	return nodeWatchLimitsCommand("/etc/sysctl.d/99-kubephos-inotify.conf")
}

func nodeWatchLimitsCommand(profile string) string {
	script := `set -eu
instances=$(sysctl -n fs.inotify.max_user_instances)
watches=$(sysctl -n fs.inotify.max_user_watches)
if [ "$instances" -lt 8192 ]; then instances=8192; fi
if [ "$watches" -lt 1048576 ]; then watches=1048576; fi
printf 'fs.inotify.max_user_instances = %s\nfs.inotify.max_user_watches = %s\n' "$instances" "$watches" > ` + shellQuote(profile) + `
sysctl -p ` + shellQuote(profile) + ` >/dev/null`
	return "sudo -n sh -c " + shellQuote(script)
}
