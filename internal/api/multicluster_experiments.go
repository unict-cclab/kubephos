package api

import (
	"context"
	"net/http"

	"kubephos.dev/kubephos/internal/domain"
)

// Application placement across members is not implemented yet.
// Until it is supported, never fall back to the single-cluster deployer or
// replicate an application implicitly on every cluster.
func (s *Server) multiClusterExperimentPipeline(_ context.Context, _ domain.ExperimentConfiguration, _ experimentConfigurationDefinitionInput, _ domain.ManagedResource) (domain.PipelineDefinition, *managedOperationFailure) {
	return domain.PipelineDefinition{}, &managedOperationFailure{
		status:  http.StatusConflict,
		code:    "multicluster_execution_unavailable",
		message: "Multi cluster experiment execution is not supported yet.",
	}
}
