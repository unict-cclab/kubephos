package domain

import (
	"encoding/json"
	"time"
)

const (
	OperationReady       = "ready"
	OperationQueued      = "queued"
	OperationPrechecking = "prechecking"
	OperationRunning     = "running"
	OperationVerifying   = "verifying"
	OperationSucceeded   = "succeeded"
	OperationFailed      = "failed"
	OperationCanceled    = "canceled"

	StepPending     = "pending"
	StepPrechecking = "prechecking"
	StepRunning     = "running"
	StepVerifying   = "verifying"
	StepSucceeded   = "succeeded"
	StepFailed      = "failed"
	StepCanceled    = "canceled"

	HealthUnknown   = "unknown"
	HealthHealthy   = "healthy"
	HealthDegraded  = "degraded"
	HealthUnhealthy = "unhealthy"
)

type Workspace struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type Credential struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type ProviderConnection struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Provider      string          `json:"provider"`
	PluginID      string          `json:"pluginId"`
	Configuration json.RawMessage `json:"-"`
	CreatedAt     time.Time       `json:"createdAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
}

type CatalogApplication struct {
	ID          string          `json:"id"`
	Reference   string          `json:"reference"`
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	Description string          `json:"description"`
	Origin      string          `json:"origin"`
	Descriptor  json.RawMessage `json:"descriptor"`
	Digest      string          `json:"digest"`
	Enabled     bool            `json:"enabled"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

type EncryptedCredential struct {
	Credential
	Nonce      []byte
	Ciphertext []byte
}

type DiscoveredResource struct {
	ExternalID string          `json:"externalId"`
	Kind       string          `json:"kind"`
	Name       string          `json:"name"`
	State      string          `json:"state"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

type DiscoveryResult struct {
	Resources []DiscoveredResource `json:"resources"`
}

type InfrastructureResource struct {
	ID          string          `json:"id"`
	Provider    string          `json:"provider"`
	ExternalID  string          `json:"externalId"`
	WorkspaceID string          `json:"workspaceId,omitempty"`
	Kind        string          `json:"kind"`
	Name        string          `json:"name"`
	State       string          `json:"state"`
	Ownership   string          `json:"ownership"`
	Protection  string          `json:"protection"`
	Metadata    json.RawMessage `json:"metadata"`
	LastSeenAt  time.Time       `json:"lastSeenAt"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

type AuditEvent struct {
	Sequence   int64           `json:"sequence"`
	Actor      string          `json:"actor"`
	Action     string          `json:"action"`
	TargetType string          `json:"targetType"`
	TargetID   string          `json:"targetId,omitempty"`
	Outcome    string          `json:"outcome"`
	Details    json.RawMessage `json:"details"`
	CreatedAt  time.Time       `json:"createdAt"`
}

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type Session struct {
	TokenHash string
	CSRFToken string
	ExpiresAt time.Time
	User      User
}

type Operation struct {
	ID              string           `json:"id"`
	WorkspaceID     string           `json:"workspaceId"`
	PluginID        string           `json:"pluginId"`
	Title           string           `json:"title"`
	Status          string           `json:"status"`
	Spec            json.RawMessage  `json:"spec"`
	Plan            Plan             `json:"plan"`
	Validation      ValidationReport `json:"validation"`
	PlanHash        string           `json:"planHash"`
	CancelRequested bool             `json:"cancelRequested"`
	Error           string           `json:"error,omitempty"`
	CreatedAt       time.Time        `json:"createdAt"`
	QueuedAt        *time.Time       `json:"queuedAt,omitempty"`
	StartedAt       *time.Time       `json:"startedAt,omitempty"`
	CompletedAt     *time.Time       `json:"completedAt,omitempty"`
	Steps           []OperationStep  `json:"steps,omitempty"`
	Artifacts       []Artifact       `json:"artifacts,omitempty"`
}

type OperationStep struct {
	ID          string          `json:"id"`
	OperationID string          `json:"operationId"`
	Position    int             `json:"position"`
	Name        string          `json:"name"`
	Status      string          `json:"status"`
	Input       json.RawMessage `json:"input"`
	Result      json.RawMessage `json:"result,omitempty"`
	Health      json.RawMessage `json:"health,omitempty"`
	Error       string          `json:"error,omitempty"`
	StartedAt   *time.Time      `json:"startedAt,omitempty"`
	CompletedAt *time.Time      `json:"completedAt,omitempty"`
}

type Plan struct {
	PluginID string     `json:"pluginId"`
	Steps    []PlanStep `json:"steps"`
}

type PlanStep struct {
	ID             string                      `json:"id"`
	Name           string                      `json:"name"`
	Input          json.RawMessage             `json:"input"`
	ArtifactInputs []ArtifactInput             `json:"artifactInputs,omitempty"`
	Outputs        []ArtifactOutput            `json:"outputs,omitempty"`
	ResolvedInputs map[string]ResolvedArtifact `json:"resolvedInputs,omitempty"`
	Effects        []ResourceEffect            `json:"effects,omitempty"`
}

type ArtifactInput struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Version    string `json:"version"`
	FromStep   string `json:"fromStep"`
	FromOutput string `json:"fromOutput"`
}

type ArtifactContract struct {
	Type    string `json:"type"`
	Version string `json:"version"`
}

type ArtifactOutput struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Version   string `json:"version"`
	MediaType string `json:"mediaType"`
	Source    string `json:"source"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

type ResolvedArtifact struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Version   string          `json:"version"`
	MediaType string          `json:"mediaType"`
	Digest    string          `json:"digest"`
	SizeBytes int64           `json:"sizeBytes"`
	Sensitive bool            `json:"sensitive"`
	Value     json.RawMessage `json:"value,omitempty"`
}

type ResourceEffect struct {
	Action     string `json:"action"`
	ExternalID string `json:"externalId"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
}

type ValidationReport struct {
	Valid     bool              `json:"valid"`
	Issues    []ValidationIssue `json:"issues"`
	CheckedAt time.Time         `json:"checkedAt"`
}

type ValidationIssue struct {
	Level   string `json:"level"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

type HealthReport struct {
	Status  string            `json:"status"`
	Summary string            `json:"summary"`
	Checks  map[string]string `json:"checks"`
}

type LogEntry struct {
	Sequence    int64     `json:"sequence"`
	OperationID string    `json:"operationId"`
	StepID      string    `json:"stepId,omitempty"`
	Level       string    `json:"level"`
	Source      string    `json:"source"`
	Message     string    `json:"message"`
	CreatedAt   time.Time `json:"createdAt"`
}

type Artifact struct {
	ID              string    `json:"id"`
	OperationID     string    `json:"operationId"`
	StepID          string    `json:"stepId,omitempty"`
	OutputName      string    `json:"outputName,omitempty"`
	Name            string    `json:"name"`
	Type            string    `json:"type"`
	Version         string    `json:"version"`
	MediaType       string    `json:"mediaType"`
	StorageKey      string    `json:"-"`
	Digest          string    `json:"digest"`
	SizeBytes       int64     `json:"sizeBytes"`
	StorageDigest   string    `json:"-"`
	StoredSizeBytes int64     `json:"-"`
	EncryptionNonce []byte    `json:"-"`
	Sensitive       bool      `json:"sensitive"`
	VerifiedAt      time.Time `json:"verifiedAt"`
	CreatedAt       time.Time `json:"createdAt"`
}

type DashboardStats struct {
	Workspaces       int `json:"workspaces"`
	ActiveOperations int `json:"activeOperations"`
	ReadyOperations  int `json:"readyOperations"`
	FailedOperations int `json:"failedOperations"`
}
