package k3s

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"kubephos.dev/kubephos/internal/placement"
)

func machineTaints(spec Spec, name string) []placement.Taint {
	for _, pool := range spec.NodePools {
		if pool.Role == "application" {
			for i := 1; i <= pool.Count; i++ {
				if name == fmt.Sprintf("%s-%s-%d", spec.ClusterName, pool.Name, i) {
					return pool.Taints
				}
			}
		}
	}
	return nil
}

// Only application pool settings are reconciled. System and unmanaged metadata
// are never removed, even if a previous workflow contains unexpected fields.
func nodeSettingsPatch(labels map[string]string, taints []map[string]any, before, after placement.PoolSettings) map[string]any {
	patch := map[string]any{}
	changedLabels := map[string]any{}
	for key, value := range after.Labels {
		if !placement.ManagedKey(key) && labels[key] != value {
			changedLabels[key] = value
		} else if !placement.ManagedKey(key) {
			if _, ok := labels[key]; !ok {
				changedLabels[key] = value
			}
		}
	}
	for key, old := range before.Labels {
		if _, keep := after.Labels[key]; !keep && !placement.ManagedKey(key) {
			if current, exists := labels[key]; exists && current == old {
				changedLabels[key] = nil
			}
		}
	}
	if len(changedLabels) > 0 {
		patch["metadata"] = map[string]any{"labels": changedLabels}
	}
	next := make([]map[string]any, 0, len(taints)+len(after.Taints))
	desired := map[string]placement.Taint{}
	previous := map[string]placement.Taint{}
	for _, t := range before.Taints {
		if !placement.ManagedTaintKey(t.Key) {
			previous[t.Key+"/"+t.Effect] = t
		}
	}
	for _, t := range after.Taints {
		if !placement.ManagedTaintKey(t.Key) {
			desired[t.Key+"/"+t.Effect] = t
		}
	}
	for _, t := range taints {
		key := fmt.Sprint(t["key"]) + "/" + fmt.Sprint(t["effect"])
		if d, ok := desired[key]; ok {
			if fmt.Sprint(t["value"]) == d.Value || t["value"] == nil && d.Value == "" {
				next = append(next, t)
			} else {
				next = append(next, taintMap(d))
			}
			delete(desired, key)
			continue
		}
		if old, ok := previous[key]; ok && (t["value"] == old.Value || t["value"] == nil && old.Value == "") {
			continue
		}
		next = append(next, t)
	}
	for _, t := range after.Taints {
		if _, ok := desired[t.Key+"/"+t.Effect]; ok {
			next = append(next, taintMap(t))
		}
	}
	if !reflect.DeepEqual(taints, next) && (len(taints) > 0 || len(next) > 0) {
		patch["spec"] = map[string]any{"taints": next}
	}
	return patch
}
func taintMap(t placement.Taint) map[string]any {
	v := map[string]any{"key": t.Key, "effect": t.Effect}
	if t.Value != "" {
		v["value"] = t.Value
	}
	return v
}

func reconcileNodeSettings(ctx context.Context, runner commandRunner, primary machine, machines []machine, access machineAccess, spec Spec) error {
	type node struct {
		Metadata struct {
			Name            string            `json:"name"`
			ResourceVersion string            `json:"resourceVersion"`
			Labels          map[string]string `json:"labels"`
		} `json:"metadata"`
		Spec struct {
			Taints []map[string]any `json:"taints"`
		} `json:"spec"`
	}
	desired := map[string]placement.PoolSettings{}
	for _, pool := range spec.NodePools {
		if pool.Role == "application" {
			for i := 1; i <= pool.Count; i++ {
				desired[fmt.Sprintf("%s-%s-%d", spec.ClusterName, pool.Name, i)] = placement.PoolSettings{Labels: pool.Labels, Taints: pool.Taints}
			}
		}
	}
	changed := false
	for name, after := range desired {
		before := spec.PreviousNodeSettings[name]
		if len(after.Labels)+len(after.Taints)+len(before.Labels)+len(before.Taints) > 0 {
			changed = true
		}
	}
	if !changed {
		return nil
	}
	raw, err := runner.Run(ctx, primary, access, "sudo k3s kubectl get nodes -o json")
	if err != nil {
		return fmt.Errorf("Could not read node scheduling settings: %w", err)
	}
	var inventory struct {
		Items []node `json:"items"`
	}
	if err = json.Unmarshal([]byte(raw), &inventory); err != nil {
		return fmt.Errorf("Could not read node scheduling settings: %w", err)
	}
	byName := map[string]node{}
	for _, n := range inventory.Items {
		byName[n.Metadata.Name] = n
	}
	for index, m := range machines {
		after, ok := desired[m.Name]
		if !ok {
			continue
		}
		before := spec.PreviousNodeSettings[m.Name]
		n, ok := byName[m.Name]
		if !ok {
			return fmt.Errorf("Application node %s is unavailable.", m.Name)
		}
		// Persist registration flags before patching live metadata. No kubelet restart
		// is needed: the flags are used only if the node subsequently registers again.
		if spec.Reconcile && !reflect.DeepEqual(before, after) {
			if _, err = runner.Run(ctx, m, access, nodeRegistrationCommand(machineLabels(spec, index), after.Taints, before)); err != nil {
				return fmt.Errorf("Could not save scheduling settings on %s: %w", m.Name, err)
			}
		}
		patch := nodeSettingsPatch(n.Metadata.Labels, n.Spec.Taints, before, after)
		if len(patch) == 0 {
			continue
		}
		metadata, _ := patch["metadata"].(map[string]any)
		if metadata == nil {
			metadata = map[string]any{}
			patch["metadata"] = metadata
		}
		metadata["resourceVersion"] = n.Metadata.ResourceVersion
		value, _ := json.Marshal(patch)
		if _, err = runner.Run(ctx, primary, access, "sudo k3s kubectl patch node "+shellQuote(m.Name)+" --type=merge -p "+shellQuote(string(value))); err != nil {
			return fmt.Errorf("Could not apply scheduling settings to %s: %w", m.Name, err)
		}
	}
	return nil
}

func nodeRegistrationCommand(labels map[string]string, taints []placement.Taint, previous ...placement.PoolSettings) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	args := []string{}
	for _, key := range keys {
		args = append(args, "--node-label", key+"="+labels[key])
	}
	for _, t := range taints {
		value := t.Key
		if t.Value != "" {
			value += "=" + t.Value
		}
		args = append(args, "--node-taint", value+":"+t.Effect)
	}
	before := placement.PoolSettings{}
	if len(previous) > 0 {
		before = previous[0]
	}
	raw, _ := json.Marshal(map[string]any{"args": args, "previous": before})
	return "set -eu\nsudo python3 - " + shellQuote(base64.StdEncoding.EncodeToString(raw)) + " <<'KUBEPHOS_NODE_SETTINGS'\n" + nodeRegistrationScript + "\nKUBEPHOS_NODE_SETTINGS\nsudo systemctl daemon-reload"
}

const nodeRegistrationScript = `import base64,json,pathlib,re,shlex,sys
path=pathlib.Path('/etc/systemd/system/k3s-agent.service')
text=path.read_text()
match=re.search(r'^ExecStart=((?:[^\n]*\\\n)*[^\n]*)',text,re.M)
if not match:
    raise SystemExit('K3s agent registration command is unavailable')
args=shlex.split(match.group(1).replace('\\\n',' '))
settings=json.loads(base64.b64decode(sys.argv[1]))
extra=settings['args']
remove_labels=set(settings.get('previous',{}).get('labels',{}))
remove_taints={(t['key'],t['effect']) for t in settings.get('previous',{}).get('taints',[])}
for flag,value in zip(extra[::2],extra[1::2]):
    if flag=='--node-label': remove_labels.add(value.split('=',1)[0])
    if flag=='--node-taint': remove_taints.add((value.rsplit(':',1)[0].split('=',1)[0],value.rsplit(':',1)[-1]))
kept=[]
i=0
while i<len(args):
    arg=args[i]
    flag=arg.split('=',1)[0]
    if flag in ('--node-label','--node-taint'):
        inline='=' in arg
        value=arg.split('=',1)[1] if inline else args[i+1]
        owned=(value.split('=',1)[0] in remove_labels) if flag=='--node-label' else ((value.rsplit(':',1)[0].split('=',1)[0],value.rsplit(':',1)[-1]) in remove_taints)
        if not owned:
            kept.extend([arg] if inline else [arg,value])
        i+=1 if inline else 2
        continue
    kept.append(arg)
    i+=1
kept+=extra
# systemd command lines use their own quoting and specifier expansion.
def quote(value):
    return '"'+value.replace('\\','\\\\').replace('"','\\"').replace('%','%%').replace('$','$$')+'"'
command='ExecStart='+' '.join(quote(value) for value in kept)
updated=text[:match.start()]+command+text[match.end():]
if updated!=text:
    temporary=path.with_suffix('.service.kubephos.tmp')
    temporary.write_text(updated)
    temporary.chmod(path.stat().st_mode & 0o777)
    temporary.replace(path)
`
