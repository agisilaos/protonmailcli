package model

import (
	"encoding/json"
	"time"
)

type Draft struct {
	ID        string     `json:"id"`
	To        []string   `json:"to"`
	CC        []string   `json:"cc,omitempty"`
	BCC       []string   `json:"bcc,omitempty"`
	Subject   string     `json:"subject"`
	Body      string     `json:"body"`
	Tags      []string   `json:"tags,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	SentAt    *time.Time `json:"sentAt,omitempty"`
}

type Message struct {
	ID      string    `json:"id"`
	DraftID string    `json:"draftId,omitempty"`
	From    string    `json:"from"`
	To      []string  `json:"to"`
	Subject string    `json:"subject"`
	Body    string    `json:"body"`
	Tags    []string  `json:"tags,omitempty"`
	SentAt  time.Time `json:"sentAt"`
}

type Filter struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Contains  string    `json:"contains"`
	AddTag    string    `json:"addTag"`
	CreatedAt time.Time `json:"createdAt"`
}

type AuthState struct {
	LoggedIn     bool       `json:"loggedIn"`
	Username     string     `json:"username,omitempty"`
	PasswordFile string     `json:"passwordFile,omitempty"`
	LastLoginAt  *time.Time `json:"lastLoginAt,omitempty"`
	LastLogoutAt *time.Time `json:"lastLogoutAt,omitempty"`
}

type IdempotencyRecord struct {
	Operation   string          `json:"operation"`
	PayloadHash string          `json:"payloadHash"`
	Response    json.RawMessage `json:"response"`
	CreatedAt   time.Time       `json:"createdAt"`
}

type BridgeState struct {
	ActiveUsername string `json:"activeUsername,omitempty"`
}

type State struct {
	Drafts      map[string]Draft             `json:"drafts"`
	Messages    map[string]Message           `json:"messages"`
	Tags        map[string]string            `json:"tags"`
	Filters     map[string]Filter            `json:"filters"`
	Auth        AuthState                    `json:"auth"`
	Bridge      BridgeState                  `json:"bridge"`
	Idempotency map[string]IdempotencyRecord `json:"idempotency"`
}
