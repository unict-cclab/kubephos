package api

import k3s "kubephos.dev/kubephos/plugins/k3s/runtime"

// Use the runtime's JSON contract: absent or empty labels are omitted, never null.
func managedClusterNodePools(input managedKubernetesClusterInput) []k3s.NodePool {
	pools := []k3s.NodePool{{Name: input.ManagementPool.Name, Role: "management", Count: input.ManagementPool.Count, Zones: input.ManagementPool.Zones, Labels: input.ManagementPool.Labels}}
	for _, pool := range input.ApplicationPools {
		pools = append(pools, k3s.NodePool{Name: pool.Name, Role: "application", Count: pool.Count, Zones: pool.Zones, Labels: pool.Labels})
	}
	return pools
}
