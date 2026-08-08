package services

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// EnvUnlockTTL is how long a minted unlock token stays valid. It matches the
// 5-minute window the frontend already advertises in the unlock dialog
// ("reveal sensitive values for the next 5 minutes").
const EnvUnlockTTL = 5 * time.Minute

// envUnlockToken is one minted token: who it was minted for, and when it dies.
type envUnlockToken struct {
	userID    string
	expiresAt time.Time
}

// EnvUnlockStore holds the short-lived tokens minted by a successful password
// re-check. A token is the second factor in front of every secret-reveal
// surface (stack .env, global env): the session cookie proves who you are, the
// unlock token proves you re-entered your password recently.
//
// A token is reusable within its TTL rather than single-use, because the UI
// deliberately prompts once and then reveals freely for the rest of the window
// — single-use would force a prompt per reveal.
//
// Tokens are held in memory only. A 5-minute window surviving a restart is not
// a requirement, and losing them on restart fails closed (the next reveal
// re-prompts), which is the safe direction.
type EnvUnlockStore struct {
	mu     sync.Mutex
	ttl    time.Duration
	tokens map[string]envUnlockToken
	// now is injectable so tests can advance the clock without sleeping.
	now func() time.Time
}

func NewEnvUnlockStore() *EnvUnlockStore {
	return &EnvUnlockStore{
		ttl:    EnvUnlockTTL,
		tokens: make(map[string]envUnlockToken),
		now:    time.Now,
	}
}

// Mint issues a new unlock token for userID and returns it with its lifetime.
// Every call issues a fresh token; an earlier one stays valid until it expires,
// so a second tab that unlocked separately is not logged out by the first.
func (s *EnvUnlockStore) Mint(userID string) (string, time.Duration, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", 0, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	s.mu.Lock()
	defer s.mu.Unlock()
	// Prune on write. The map only ever holds tokens minted in the last TTL
	// plus whatever expired since the previous mint, so it stays small without
	// a background goroutine.
	s.pruneLocked()
	s.tokens[token] = envUnlockToken{userID: userID, expiresAt: s.now().Add(s.ttl)}

	return token, s.ttl, nil
}

// Valid reports whether token is a live token minted for userID. Binding to the
// user matters: without it, any authenticated caller could replay a token
// another account minted.
func (s *EnvUnlockStore) Valid(token, userID string) bool {
	if token == "" || userID == "" {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.tokens[token]
	if !ok {
		return false
	}
	if s.now().After(entry.expiresAt) {
		delete(s.tokens, token)
		return false
	}

	return entry.userID == userID
}

// RevokeUser drops every token minted for userID before its natural expiry.
// Called on logout so a live unlock window cannot outlive the session that
// opened it.
func (s *EnvUnlockStore) RevokeUser(userID string) {
	if userID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, entry := range s.tokens {
		if entry.userID == userID {
			delete(s.tokens, token)
		}
	}
}

func (s *EnvUnlockStore) pruneLocked() {
	now := s.now()
	for token, entry := range s.tokens {
		if now.After(entry.expiresAt) {
			delete(s.tokens, token)
		}
	}
}
