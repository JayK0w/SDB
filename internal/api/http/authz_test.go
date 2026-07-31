package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	hub := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	return NewServer(
		Options{Addr: "127.0.0.1:0", JWTSecret: "test-secret-value", TokenTTL: time.Hour},
		Services{}, hub, slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
}

func tokenFor(t *testing.T, s *Server, role domain.Role) string {
	t.Helper()
	tok, _, err := s.tokens.Issue(&domain.User{ID: 1, Username: "u", Role: role})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func do(s *Server, method, path, token string, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.http.Handler.ServeHTTP(w, r)
	return w
}

// Une restauration écrase des données de production : elle doit être fermée
// au rôle `user`, quel que soit son contenu.
func TestRestoreEndpointsRequireAdmin(t *testing.T) {
	s := testServer(t)
	userTok := tokenFor(t, s, domain.RoleUser)

	cases := []struct {
		method, path, body string
	}{
		{http.MethodPost, "/api/v1/restores", `{"storage_id":1,"snapshot_id":"s","target_volume":"v"}`},
		{http.MethodDelete, "/api/v1/restores/1", ""},
		{http.MethodGet, "/api/v1/restores/clone-compose?container_id=c&source_volume=a&target_volume=b", ""},
	}
	for _, tc := range cases {
		w := do(s, tc.method, tc.path, userTok, tc.body)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s %s as user -> %d, want 403", tc.method, tc.path, w.Code)
		}
	}
}

// La lecture de l'historique reste ouverte : restreindre l'écriture ne doit
// pas aveugler les comptes non-admin.
func TestRestoreHistoryStaysReadableByUsers(t *testing.T) {
	s := testServer(t)
	w := do(s, http.MethodGet, "/api/v1/restores/history", tokenFor(t, s, domain.RoleUser), "")
	if w.Code == http.StatusForbidden {
		t.Fatal("restore history must stay readable for the user role")
	}
}

func TestRestoreEndpointsRejectAnonymous(t *testing.T) {
	s := testServer(t)
	w := do(s, http.MethodPost, "/api/v1/restores", "", `{"storage_id":1}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous restore -> %d, want 401", w.Code)
	}
}

func TestSecurityHeadersArePresent(t *testing.T) {
	s := testServer(t)
	// requête anonyme : les en-têtes sont posés avant l'authentification, et
	// la réponse 401 n'atteint aucun usecase (Services est vide ici).
	w := do(s, http.MethodGet, "/api/v1/health", "", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("setup: expected 401, got %d", w.Code)
	}

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for k, v := range want {
		if got := w.Header().Get(k); got != v {
			t.Fatalf("header %s = %q, want %q", k, got, v)
		}
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("CSP missing or too permissive: %q", csp)
	}
	// HSTS en clair verrouillerait le navigateur sur un https inexistant
	if hsts := w.Header().Get("Strict-Transport-Security"); hsts != "" {
		t.Fatalf("HSTS must not be sent over plaintext, got %q", hsts)
	}
}

// Sans plafond, un client authentifié fait gonfler la mémoire du processus
// à volonté.
func TestOversizedBodyIsRejected(t *testing.T) {
	s := testServer(t)
	huge := `{"storage_id":1,"snapshot_id":"` + strings.Repeat("A", maxBodyBytes+1024) + `"}`

	w := do(s, http.MethodPost, "/api/v1/restores", tokenFor(t, s, domain.RoleAdmin), huge)
	if w.Code != http.StatusBadRequest && w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body -> %d, want 400 or 413", w.Code)
	}
}
