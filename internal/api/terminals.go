package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"
	"kubephos.dev/kubephos/internal/pluginssh"
	"kubephos.dev/kubephos/internal/storage"
)

type terminalTicket struct {
	ID               string
	UserID           string
	WorkspaceID      string
	MachineSetRef    string
	MachineAccessRef string
	MachineIndex     int
	Columns          int
	Rows             int
	ExpiresAt        time.Time
}

type terminalMachineSet struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Spec       struct {
		Machines []struct {
			Name    string `json:"name"`
			Address string `json:"address"`
			SSHPort int    `json:"sshPort"`
			SSHUser string `json:"sshUser"`
			State   string `json:"state"`
		} `json:"machines"`
	} `json:"spec"`
}

type terminalMachineAccess struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Spec       struct {
		Algorithm  string `json:"algorithm"`
		PrivateKey string `json:"privateKey"`
	} `json:"spec"`
}

type terminalTarget struct {
	WorkspaceID      string
	MachineSetRef    string
	MachineAccessRef string
	MachineIndex     int
	Name             string
	SSH              pluginssh.Target
	PrivateKey       string
}

type terminalMessage struct {
	Type    string `json:"type"`
	Data    string `json:"data,omitempty"`
	Columns int    `json:"columns,omitempty"`
	Rows    int    `json:"rows,omitempty"`
}

func (s *Server) listTerminalTargets(response http.ResponseWriter, request *http.Request) {
	if !s.terminalCapabilityAvailable() {
		writeError(response, http.StatusServiceUnavailable, "terminal_capability_unavailable", "Install a compatible terminal capability first.")
		return
	}
	workspaceID := strings.TrimSpace(request.URL.Query().Get("workspaceId"))
	if workspaceID == "" {
		writeError(response, http.StatusBadRequest, "invalid_workspace", "Select a workspace.")
		return
	}
	if _, err := s.store.GetWorkspace(request.Context(), workspaceID); errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Workspace not found.")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not validate the workspace.")
		return
	}
	artifacts, err := s.store.ListAvailableArtifacts(request.Context(), 500)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list terminal targets.")
		return
	}
	accessByOperation := map[string]string{}
	for _, artifact := range artifacts {
		if artifact.WorkspaceID == workspaceID && artifact.Type == "MachineAccess" && artifact.Version == "v1alpha1" && artifact.Sensitive {
			accessByOperation[artifact.OperationID] = artifact.ID
		}
	}
	items := []map[string]any{}
	for _, artifact := range artifacts {
		accessRef := accessByOperation[artifact.OperationID]
		if artifact.WorkspaceID != workspaceID || artifact.Type != "MachineSet" || artifact.Version != "v1alpha1" || artifact.Sensitive || accessRef == "" {
			continue
		}
		value, err := s.readArtifactPayload(request.Context(), artifact)
		if err != nil {
			writeError(response, http.StatusConflict, "artifact_integrity_failed", "A terminal target failed integrity verification.")
			return
		}
		var machines terminalMachineSet
		if err := json.Unmarshal(value, &machines); err != nil || machines.APIVersion != "artifacts.kubephos.dev/v1alpha1" || machines.Kind != "MachineSet" {
			continue
		}
		targets := []map[string]any{}
		for index, machine := range machines.Spec.Machines {
			if machine.Name != "" && machine.State == "running" {
				targets = append(targets, map[string]any{"index": index, "name": machine.Name, "state": machine.State})
			}
		}
		if len(targets) > 0 {
			items = append(items, map[string]any{"machineSetRef": artifact.ID, "machineAccessRef": accessRef, "name": artifact.Name, "machines": targets})
		}
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createTerminal(response http.ResponseWriter, request *http.Request) {
	if !s.terminalCapabilityAvailable() {
		writeError(response, http.StatusServiceUnavailable, "terminal_capability_unavailable", "Install a compatible terminal capability first.")
		return
	}
	var input struct {
		WorkspaceID      string `json:"workspaceId"`
		MachineSetRef    string `json:"machineSetRef"`
		MachineAccessRef string `json:"machineAccessRef"`
		MachineIndex     int    `json:"machineIndex"`
		Columns          int    `json:"columns"`
		Rows             int    `json:"rows"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.MachineSetRef = strings.TrimSpace(input.MachineSetRef)
	input.MachineAccessRef = strings.TrimSpace(input.MachineAccessRef)
	if input.Columns < 20 || input.Columns > 400 || input.Rows < 5 || input.Rows > 200 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_terminal_size", "Terminal dimensions are outside the supported range.")
		return
	}
	target, err := s.loadTerminalTarget(request.Context(), input.WorkspaceID, input.MachineSetRef, input.MachineAccessRef, input.MachineIndex)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_terminal_target", err.Error())
		return
	}
	user, ok := request.Context().Value(authenticatedUserKey{}).(domain.User)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required", "Sign in to continue.")
		return
	}
	now := time.Now()
	s.terminalMu.Lock()
	for ticketID, ticket := range s.terminals {
		if ticket.ExpiresAt.Before(now) {
			delete(s.terminals, ticketID)
		}
	}
	if len(s.terminals) >= 100 {
		s.terminalMu.Unlock()
		writeError(response, http.StatusTooManyRequests, "terminal_capacity", "Too many terminal sessions are waiting to connect.")
		return
	}
	ticket := terminalTicket{ID: id.New("term"), UserID: user.ID, WorkspaceID: target.WorkspaceID, MachineSetRef: target.MachineSetRef, MachineAccessRef: target.MachineAccessRef, MachineIndex: target.MachineIndex, Columns: input.Columns, Rows: input.Rows, ExpiresAt: now.Add(30 * time.Second)}
	s.terminals[ticket.ID] = ticket
	s.terminalMu.Unlock()
	writeJSON(response, http.StatusCreated, map[string]any{"id": ticket.ID, "connectPath": "/api/v1/terminals/" + ticket.ID + "/connect", "target": target.Name, "expiresAt": ticket.ExpiresAt})
}

func (s *Server) terminalCapabilityAvailable() bool {
	for _, manifest := range s.registry.Manifests() {
		if manifest.HasCapability("infrastructure.ssh.terminal") {
			return true
		}
	}
	return false
}

func (s *Server) connectTerminal(response http.ResponseWriter, request *http.Request) {
	user, ok := request.Context().Value(authenticatedUserKey{}).(domain.User)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required", "Sign in to continue.")
		return
	}
	now := time.Now()
	s.terminalMu.Lock()
	ticket, exists := s.terminals[request.PathValue("id")]
	if exists {
		delete(s.terminals, ticket.ID)
	}
	s.terminalMu.Unlock()
	if !exists || ticket.ExpiresAt.Before(now) || ticket.UserID != user.ID {
		writeError(response, http.StatusNotFound, "terminal_ticket_invalid", "The terminal ticket is invalid or expired.")
		return
	}
	target, err := s.loadTerminalTarget(request.Context(), ticket.WorkspaceID, ticket.MachineSetRef, ticket.MachineAccessRef, ticket.MachineIndex)
	if err != nil {
		s.auditTerminal(request, ticket.ID, "rejected", "", ticket, 0)
		writeError(response, http.StatusUnprocessableEntity, "invalid_terminal_target", err.Error())
		return
	}
	if !s.acquireTerminal(user.ID) {
		s.auditTerminal(request, ticket.ID, "rejected", target.Name, ticket, 0)
		writeError(response, http.StatusTooManyRequests, "terminal_capacity", "The terminal session limit has been reached.")
		return
	}
	defer s.releaseTerminal(user.ID)
	sshTerminal, err := s.terminalSSH.OpenTerminal(request.Context(), target.SSH, target.PrivateKey, ticket.Columns, ticket.Rows)
	if err != nil {
		s.auditTerminal(request, ticket.ID, "rejected", target.Name, ticket, 0)
		writeError(response, http.StatusBadGateway, "ssh_unavailable", "The managed SSH target is unavailable.")
		return
	}
	connection, err := websocket.Accept(response, request, nil)
	if err != nil {
		sshTerminal.Close()
		return
	}
	started := time.Now()
	s.auditTerminal(request, ticket.ID, "opened", target.Name, ticket, 0)
	s.bridgeTerminal(request.Context(), connection, sshTerminal)
	duration := time.Since(started)
	s.auditTerminal(request, ticket.ID, "closed", target.Name, ticket, duration)
}

func (s *Server) acquireTerminal(userID string) bool {
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	total := 0
	for _, count := range s.terminalActive {
		total += count
	}
	if total >= 32 || s.terminalActive[userID] >= 4 {
		return false
	}
	s.terminalActive[userID]++
	return true
}

func (s *Server) releaseTerminal(userID string) {
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	if s.terminalActive[userID] <= 1 {
		delete(s.terminalActive, userID)
		return
	}
	s.terminalActive[userID]--
}

func (s *Server) bridgeTerminal(parent context.Context, connection *websocket.Conn, terminal *pluginssh.Terminal) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Hour)
	defer cancel()
	defer connection.Close(websocket.StatusNormalClosure, "Terminal session closed")
	defer terminal.Close()
	connection.SetReadLimit(64 * 1024)
	var activity atomic.Int64
	activity.Store(time.Now().UnixNano())
	completed := make(chan error, 4)
	go func() {
		buffer := make([]byte, 32*1024)
		for {
			count, err := terminal.Read(buffer)
			if count > 0 {
				activity.Store(time.Now().UnixNano())
				if writeErr := connection.Write(ctx, websocket.MessageBinary, buffer[:count]); writeErr != nil {
					completed <- writeErr
					return
				}
			}
			if err != nil {
				completed <- err
				return
			}
		}
	}()
	go func() {
		for {
			messageType, value, err := connection.Read(ctx)
			if err != nil {
				completed <- err
				return
			}
			if messageType != websocket.MessageText {
				completed <- errors.New("terminal input must use text messages")
				return
			}
			var message terminalMessage
			if err := json.Unmarshal(value, &message); err != nil {
				completed <- errors.New("terminal message is invalid")
				return
			}
			activity.Store(time.Now().UnixNano())
			switch message.Type {
			case "input":
				if len(message.Data) > 32*1024 {
					completed <- errors.New("terminal input is too large")
					return
				}
				if _, err := terminal.Write([]byte(message.Data)); err != nil {
					completed <- err
					return
				}
			case "resize":
				if message.Columns < 20 || message.Columns > 400 || message.Rows < 5 || message.Rows > 200 {
					completed <- errors.New("terminal dimensions are invalid")
					return
				}
				if err := terminal.Resize(message.Columns, message.Rows); err != nil {
					completed <- err
					return
				}
			default:
				completed <- errors.New("terminal message type is invalid")
				return
			}
		}
	}()
	go func() { completed <- terminal.Wait() }()
	go func() {
		idle := time.NewTicker(time.Minute)
		defer idle.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case tick := <-idle.C:
				last := time.Unix(0, activity.Load())
				if tick.Sub(last) >= 20*time.Minute {
					completed <- errors.New("terminal idle timeout")
					return
				}
			}
		}
	}()
	select {
	case <-ctx.Done():
	case <-completed:
	}
}

func (s *Server) loadTerminalTarget(ctx context.Context, workspaceID, machineSetRef, machineAccessRef string, machineIndex int) (terminalTarget, error) {
	if workspaceID == "" || !strings.HasPrefix(machineSetRef, "art_") || !strings.HasPrefix(machineAccessRef, "art_") || machineSetRef == machineAccessRef || machineIndex < 0 || machineIndex > 99 {
		return terminalTarget{}, errors.New("terminal target references are invalid")
	}
	machineArtifact, err := s.store.GetArtifact(ctx, machineSetRef)
	if err != nil {
		return terminalTarget{}, errors.New("machine set is unavailable")
	}
	accessArtifact, err := s.store.GetArtifact(ctx, machineAccessRef)
	if err != nil {
		return terminalTarget{}, errors.New("machine access is unavailable")
	}
	if machineArtifact.OperationID != accessArtifact.OperationID || machineArtifact.Type != "MachineSet" || machineArtifact.Version != "v1alpha1" || machineArtifact.Sensitive || accessArtifact.Type != "MachineAccess" || accessArtifact.Version != "v1alpha1" || !accessArtifact.Sensitive {
		return terminalTarget{}, errors.New("machine artifacts are incompatible")
	}
	operation, err := s.store.GetOperation(ctx, machineArtifact.OperationID)
	if err != nil || operation.Status != domain.OperationSucceeded || operation.WorkspaceID != workspaceID {
		return terminalTarget{}, errors.New("machine artifacts must belong to a successful operation in this workspace")
	}
	machineValue, err := s.readArtifactPayload(ctx, machineArtifact)
	if err != nil {
		return terminalTarget{}, fmt.Errorf("verify machine set: %w", err)
	}
	accessValue, err := s.readArtifactPayload(ctx, accessArtifact)
	if err != nil {
		return terminalTarget{}, fmt.Errorf("verify machine access: %w", err)
	}
	var machines terminalMachineSet
	var access terminalMachineAccess
	if json.Unmarshal(machineValue, &machines) != nil || machines.APIVersion != "artifacts.kubephos.dev/v1alpha1" || machines.Kind != "MachineSet" || machineIndex >= len(machines.Spec.Machines) {
		return terminalTarget{}, errors.New("machine set contract is invalid")
	}
	if json.Unmarshal(accessValue, &access) != nil || access.APIVersion != "artifacts.kubephos.dev/v1alpha1" || access.Kind != "MachineAccess" || access.Spec.Algorithm != "ssh-ed25519" || strings.TrimSpace(access.Spec.PrivateKey) == "" {
		return terminalTarget{}, errors.New("machine access contract is invalid")
	}
	machine := machines.Spec.Machines[machineIndex]
	if machine.Name == "" || machine.Address == "" || machine.SSHUser == "" || machine.SSHPort < 1 || machine.SSHPort > 65535 || machine.State != "running" {
		return terminalTarget{}, errors.New("selected machine is not a running SSH target")
	}
	return terminalTarget{WorkspaceID: workspaceID, MachineSetRef: machineSetRef, MachineAccessRef: machineAccessRef, MachineIndex: machineIndex, Name: machine.Name, SSH: pluginssh.Target{Address: machine.Address, Port: machine.SSHPort, User: machine.SSHUser}, PrivateKey: access.Spec.PrivateKey}, nil
}

func (s *Server) auditTerminal(request *http.Request, terminalID, outcome, target string, ticket terminalTicket, duration time.Duration) {
	details, _ := json.Marshal(map[string]any{"workspaceId": ticket.WorkspaceID, "machineSetRef": ticket.MachineSetRef, "machineIndex": ticket.MachineIndex, "target": target, "durationSeconds": int(duration.Seconds())})
	_ = s.store.AppendAuditEvent(context.WithoutCancel(request.Context()), domain.AuditEvent{Actor: actorName(request), Action: "terminal.session", TargetType: "ssh-terminal", TargetID: terminalID, Outcome: outcome, Details: details})
}
