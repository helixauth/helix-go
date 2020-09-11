package entity

import (
	"time"
)

// Session represents a particular OAuth/OIDC session
type Session struct {
	ID           string     `json:"id"`
	TenantID     string     `json:"tenant_id"`
	ClientID     string     `json:"client_id"`
	UserID       *string    `json:"user_id"`
	ResponseType string     `json:"response_type"`
	Scope        string     `json:"scope"`
	State        string     `json:"state"`
	Nonce        string     `json:"nonce"`
	RedirectURI  string     `json:"redirect_uri"`
	Code         string     `json:"code"`
	CreatedAt    time.Time  `json:"created_at"`
	ClaimedAt    *time.Time `json:"claimed_at"`
	RefreshedAt  *time.Time `json:"refreshed_at"`
}

// DatabaseTable points to the "sessions" table
func (s *Session) DatabaseTable() string {
	return "sessions"
}
