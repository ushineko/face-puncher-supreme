package web

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const (
	sessionCookieName = "fps_session"
	sessionLifetime   = 24 * time.Hour
)

type session struct {
	token     string
	expiresAt time.Time
}

// sessionStore manages in-memory sessions (no persistence across restarts).
type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]*session
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: make(map[string]*session)}
}

// create generates a new session token and stores it.
func (s *sessionStore) create() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)

	s.mu.Lock()
	s.sessions[token] = &session{
		token:     token,
		expiresAt: time.Now().Add(sessionLifetime),
	}
	s.mu.Unlock()

	return token, nil
}

// validate checks if a token is valid and not expired.
func (s *sessionStore) validate(token string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	sess, ok := s.sessions[token]
	s.mu.Unlock()

	if !ok {
		return false
	}
	if time.Now().After(sess.expiresAt) {
		s.revoke(token)
		return false
	}
	return true
}

// revoke removes a session.
func (s *sessionStore) revoke(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

// setSessionCookie writes the session cookie to the response.
func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/fps/",
		MaxAge:   int(sessionLifetime.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// clearSessionCookie expires the session cookie.
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/fps/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// getSessionToken extracts the session token from the request. It checks
// the "token" query parameter first, then falls back to the session cookie.
// The query param is needed for WebSocket connections through HTTP proxies
// where the browser may send a stale cookie from a previous session instead
// of the cookie set on the current direct HTTP connection.
func getSessionToken(r *http.Request) string {
	if t := r.URL.Query().Get("token"); t != "" {
		return t
	}
	c, err := r.Cookie(sessionCookieName)
	if err == nil {
		return c.Value
	}
	return ""
}
