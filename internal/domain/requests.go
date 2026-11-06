package domain

import "time"

const (
	AdminRequestOpen     = "open"
	AdminRequestResolved = "resolved"
)

// AdminRequest is a message a member sends to the administrators. It works like a
// ticket: an administrator resolves it, optionally with a single reply.
type AdminRequest struct {
	ID              string     `json:"id"`
	RequesterUserID string     `json:"requesterUserId"`
	Requester       string     `json:"requester"`
	Message         string     `json:"message"`
	Status          string     `json:"status"`
	Reply           string     `json:"reply,omitempty"`
	ResolvedBy      string     `json:"resolvedBy,omitempty"`
	ResolvedAt      *time.Time `json:"resolvedAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}
