package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
	"github.com/standalone-docker-backup/sdb/internal/usecase"
)

// stubUsers : depot utilisateurs minimal. authRequired verifie desormais la
// generation du jeton a chaque requete, il faut donc un vrai AuthService.
type stubUsers struct {
	mu    sync.Mutex
	byID  map[int64]domain.User
	calls int // lectures de version, pour verifier qu'on interroge bien la base
}

func newStubUsers(users ...domain.User) *stubUsers {
	m := &stubUsers{byID: map[int64]domain.User{}}
	for _, u := range users {
		m.byID[u.ID] = u
	}
	return m
}

func (m *stubUsers) TokenVersion(_ context.Context, id int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	u, ok := m.byID[id]
	if !ok {
		return 0, domain.ErrNotFound
	}
	return u.TokenVersion, nil
}

func (m *stubUsers) bump(id int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.byID[id]
	u.TokenVersion++
	m.byID[id] = u
}

func (m *stubUsers) remove(id int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.byID, id)
}

func (m *stubUsers) GetByID(_ context.Context, id int64) (*domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byID[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	out := u
	return &out, nil
}

func (m *stubUsers) Create(context.Context, *domain.User) error { return nil }
func (m *stubUsers) GetByUsername(context.Context, string) (*domain.User, error) {
	return nil, domain.ErrNotFound
}
func (m *stubUsers) List(context.Context) ([]domain.User, error) { return nil, nil }
func (m *stubUsers) Update(context.Context, *domain.User) error  { return nil }
func (m *stubUsers) Delete(context.Context, int64) error         { return nil }
func (m *stubUsers) Count(context.Context) (int64, error)        { return 1, nil }

func testServer(t *testing.T) (*Server, *stubUsers) {
	t.Helper()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	users := newStubUsers(
		domain.User{ID: 1, Username: "admin", Role: domain.RoleAdmin, TokenVersion: 1},
		domain.User{ID: 2, Username: "bob", Role: domain.RoleUser, TokenVersion: 1},
	)
	srv := NewServer(
		Options{Addr: "127.0.0.1:0", JWTSecret: "test-secret-value", TokenTTL: time.Hour},
		Services{Auth: usecase.NewAuthService(users, fakeHasher{}, discard)},
		NewHub(discard), discard,
	)
	return srv, users
}

type fakeHasher struct{}

func (fakeHasher) Hash(p string) (string, error)    { return "h:" + p, nil }
func (fakeHasher) Verify(p, h string) (bool, error) { return h == "h:"+p, nil }

// tokenFor : jeton portant la generation COURANTE du compte.
func tokenFor(t *testing.T, s *Server, role domain.Role) string {
	t.Helper()
	id := int64(1)
	name := "admin"
	if role == domain.RoleUser {
		id, name = 2, "bob"
	}
	tok, _, err := s.tokens.Issue(&domain.User{ID: id, Username: name, Role: role, TokenVersion: 1})
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
	s, _ := testServer(t)
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

// Les opérations lourdes sur un dépôt coûtent des I/O et de la bande passante,
// et la vérification écrit un volume jetable : elles restent fermées au rôle
// `user`, comme la gestion des dépôts elle-même.
func TestStorageOperationsRequireAdmin(t *testing.T) {
	s, _ := testServer(t)
	userTok := tokenFor(t, s, domain.RoleUser)

	cases := []struct {
		method, path string
	}{
		{http.MethodPost, "/api/v1/storage/1/check"},
		{http.MethodPost, "/api/v1/storage/1/verify"},
		{http.MethodPost, "/api/v1/storage/1/replicate"},
		{http.MethodDelete, "/api/v1/storage/1"},
	}
	for _, tc := range cases {
		if w := do(s, tc.method, tc.path, userTok, ""); w.Code != http.StatusForbidden {
			t.Fatalf("%s %s as user -> %d, want 403", tc.method, tc.path, w.Code)
		}
	}
	// et rien de tout ça n'est ouvert à un anonyme
	for _, tc := range cases {
		if w := do(s, tc.method, tc.path, "", ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s anonymous -> %d, want 401", tc.method, tc.path, w.Code)
		}
	}
}

// La lecture de l'historique reste ouverte : restreindre l'écriture ne doit
// pas aveugler les comptes non-admin.
func TestRestoreHistoryStaysReadableByUsers(t *testing.T) {
	s, _ := testServer(t)
	w := do(s, http.MethodGet, "/api/v1/restores/history", tokenFor(t, s, domain.RoleUser), "")
	if w.Code == http.StatusForbidden {
		t.Fatal("restore history must stay readable for the user role")
	}
}

func TestRestoreEndpointsRejectAnonymous(t *testing.T) {
	s, _ := testServer(t)
	w := do(s, http.MethodPost, "/api/v1/restores", "", `{"storage_id":1}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous restore -> %d, want 401", w.Code)
	}
}

func TestSecurityHeadersArePresent(t *testing.T) {
	s, _ := testServer(t)
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
	s, _ := testServer(t)
	huge := `{"storage_id":1,"snapshot_id":"` + strings.Repeat("A", maxBodyBytes+1024) + `"}`

	w := do(s, http.MethodPost, "/api/v1/restores", tokenFor(t, s, domain.RoleAdmin), huge)
	if w.Code != http.StatusBadRequest && w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body -> %d, want 400 or 413", w.Code)
	}
}
