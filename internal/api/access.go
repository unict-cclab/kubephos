package api

import (
	"net/http"
	"strings"

	"kubephos.dev/kubephos/internal/domain"
)

func requestUser(request *http.Request) domain.User {
	user, _ := request.Context().Value(authenticatedUserKey{}).(domain.User)
	return user
}

func (s *Server) authorizeExperiment(response http.ResponseWriter, request *http.Request, experimentID string) bool {
	ownerID, err := s.store.GetExperimentOwner(request.Context(), experimentID)
	if err != nil {
		writeError(response, http.StatusNotFound, "not_found", "Experiment not found.")
		return false
	}
	if !ownsResource(requestUser(request), ownerID) {
		writeError(response, http.StatusNotFound, "not_found", "Experiment not found.")
		return false
	}
	return true
}

func (s *Server) authorizeOperation(response http.ResponseWriter, request *http.Request, operationID string) bool {
	user := requestUser(request)
	if user.Role == "admin" {
		return true
	}
	owned, err := s.store.UserOwnsOperation(request.Context(), user.ID, operationID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not verify operation access.")
		return false
	}
	if !owned {
		writeError(response, http.StatusNotFound, "not_found", "Operation not found.")
		return false
	}
	return true
}

func experimentIsolationMode(definition experimentConfigurationDefinitionInput) string {
	for _, component := range definition.Components {
		capability := strings.ToLower(component.Capability)
		if strings.Contains(capability, "network") || strings.Contains(capability, "inject") || strings.Contains(capability, "scheduler") || strings.Contains(capability, "descheduler") {
			return "exclusive"
		}
	}
	return "namespaced"
}

func ownsResource(user domain.User, ownerUserID string) bool {
	return user.Role == "admin" || user.ID != "" && user.ID == ownerUserID
}

func (s *Server) validateClusterAssignment(response http.ResponseWriter, request *http.Request, user domain.User, clusterID string) bool {
	if user.Role == "admin" {
		return true
	}
	allowed, err := s.store.UserCanUseCluster(request.Context(), user.ID, clusterID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not verify cluster access.")
		return false
	}
	if !allowed {
		writeError(response, http.StatusForbidden, "cluster_not_assigned", "This Kubernetes cluster is not assigned to your account.")
		return false
	}
	return true
}
