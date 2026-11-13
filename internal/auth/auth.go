// Package auth implements registration, password login and cookie sessions.
// The first registered account becomes the administrator.
package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"ios-auto-sign/internal/secure"
	"ios-auto-sign/internal/store"
)

const cookieName = "ias_session"
const sessionTTL = 7 * 24 * time.Hour

type Manager struct {
	st           *store.Store
	secureCookie bool
}

func New(st *store.Store, publicBaseURL string) *Manager {
	return &Manager{
		st:           st,
		secureCookie: strings.HasPrefix(strings.ToLower(publicBaseURL), "https://"),
	}
}

func (m *Manager) Register(username, password string) (*store.User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if len(username) < 3 || len(username) > 64 {
		return nil, errors.New("username must be 3-64 characters")
	}
	if len(password) < 8 {
		return nil, errors.New("password must be at least 8 characters")
	}
	hash, err := secure.HashPassword(password)
	if err != nil {
		return nil, err
	}
	var out *store.User
	err = m.st.Update(func(d *store.Data) error {
		for _, u := range d.Users {
			if u.Username == username {
				return errors.New("username already exists")
			}
		}
		role := "user"
		if len(d.Users) == 0 {
			role = "admin"
		}
		u := &store.User{
			ID: store.NewID(), Username: username, PasswordHash: hash,
			Role: role, CreatedAt: time.Now(),
		}
		d.Users[u.ID] = u
		cp := *u
		out = &cp
		return nil
	})
	return out, err
}

func (m *Manager) Login(username, password string) (string, error) {
	u, ok := m.st.UserByUsername(strings.ToLower(strings.TrimSpace(username)))
	if !ok || !secure.VerifyPassword(password, u.PasswordHash) {
		return "", errors.New("invalid username or password")
	}
	token, err := secure.RandomHex(32)
	if err != nil {
		return "", err
	}
	s := &store.Session{Token: token, UserID: u.ID, ExpiresAt: time.Now().Add(sessionTTL)}
	if err := m.st.Update(func(d *store.Data) error {
		for k, v := range d.Sessions {
			if time.Now().After(v.ExpiresAt) {
				delete(d.Sessions, k)
			}
		}
		d.Sessions[token] = s
		return nil
	}); err != nil {
		return "", err
	}
	return token, nil
}

func (m *Manager) Logout(token string) error {
	return m.st.Update(func(d *store.Data) error {
		delete(d.Sessions, token)
		return nil
	})
}

func (m *Manager) CurrentUser(r *http.Request) (*store.User, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return nil, false
	}
	var out *store.User
	m.st.View(func(d *store.Data) {
		s := d.Sessions[c.Value]
		if s == nil || time.Now().After(s.ExpiresAt) {
			return
		}
		if u := d.Users[s.UserID]; u != nil {
			cp := *u
			out = &cp
		}
	})
	return out, out != nil
}

func (m *Manager) SetCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: m.secureCookie,
		Expires: time.Now().Add(sessionTTL),
	})
}

func (m *Manager) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: m.secureCookie,
		MaxAge: -1,
	})
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// Require wraps a handler that needs an authenticated user.
func (m *Manager) Require(next func(http.ResponseWriter, *http.Request, *store.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := m.CurrentUser(r)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "login required")
			return
		}
		next(w, r, u)
	}
}

// RequireAdmin wraps a handler that needs administrator privileges.
func (m *Manager) RequireAdmin(next func(http.ResponseWriter, *http.Request, *store.User)) http.HandlerFunc {
	return m.Require(func(w http.ResponseWriter, r *http.Request, u *store.User) {
		if u.Role != "admin" {
			writeErr(w, http.StatusForbidden, "administrator only")
			return
		}
		next(w, r, u)
	})
}
