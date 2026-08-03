package httpapi

import (
	"net/http"
	"testing"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// Le coeur du chantier : un jeton valablement signe doit CESSER d'etre
// accepte des que la generation du compte change. Sans ca, un jeton vole
// reste utilisable jusqu'a son expiration.
func TestTokenRejectedAfterSessionRevocation(t *testing.T) {
	s, users := testServer(t)
	tok := tokenFor(t, s, domain.RoleAdmin)

	// avant revocation : le jeton passe l'authentification
	if w := do(s, http.MethodGet, "/api/v1/restores/history", tok, ""); w.Code == http.StatusUnauthorized {
		t.Fatalf("setup: le jeton devait etre accepte, got %d", w.Code)
	}

	users.bump(1)

	w := do(s, http.MethodGet, "/api/v1/restores/history", tok, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("apres revocation -> %d, want 401 (le jeton reste signe mais perime)", w.Code)
	}
}

// Supprimer un compte doit tuer ses sessions. C'est le scenario du depart
// d'un collaborateur : sans ca, son jeton restait pleinement valide.
func TestTokenRejectedAfterAccountDeletion(t *testing.T) {
	s, users := testServer(t)
	tok := tokenFor(t, s, domain.RoleAdmin)

	users.remove(1)

	w := do(s, http.MethodGet, "/api/v1/restores/history", tok, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("compte supprime -> %d, want 401", w.Code)
	}
}

// Un jeton fabrique avec une generation arbitraire ne doit pas passer : la
// signature prouve l'emission, pas l'actualite.
func TestForgedVersionIsRejected(t *testing.T) {
	s, _ := testServer(t)

	// jeton correctement signe, mais annoncant une generation qui n'existe pas
	tok, _, err := s.tokens.Issue(&domain.User{
		ID: 1, Username: "admin", Role: domain.RoleAdmin, TokenVersion: 999,
	})
	if err != nil {
		t.Fatal(err)
	}
	w := do(s, http.MethodGet, "/api/v1/restores/history", tok, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("generation inconnue -> %d, want 401", w.Code)
	}
}

// La generation zero -- valeur par defaut de Go si le champ n'est jamais
// renseigne -- ne doit pas passer par accident.
func TestZeroVersionIsRejected(t *testing.T) {
	s, _ := testServer(t)
	tok, _, err := s.tokens.Issue(&domain.User{ID: 1, Username: "admin", Role: domain.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if w := do(s, http.MethodGet, "/api/v1/restores/history", tok, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("generation zero -> %d, want 401", w.Code)
	}
}

// La verification doit avoir lieu a CHAQUE requete, pas seulement a la
// premiere : un cache silencieux reintroduirait un delai de revocation.
func TestSessionCheckedOnEveryRequest(t *testing.T) {
	s, users := testServer(t)
	tok := tokenFor(t, s, domain.RoleAdmin)

	for i := 0; i < 3; i++ {
		do(s, http.MethodGet, "/api/v1/restores/history", tok, "")
	}
	users.mu.Lock()
	calls := users.calls
	users.mu.Unlock()
	if calls < 3 {
		t.Fatalf("%d lectures de generation pour 3 requetes : la verification est mise en cache", calls)
	}
}

// L'endpoint de revocation suit la meme regle que le changement de mot de
// passe : soi-meme ou admin.
func TestRevokeEndpointAuthorization(t *testing.T) {
	s, _ := testServer(t)
	userTok := tokenFor(t, s, domain.RoleUser) // bob, id 2

	// bob revoquant les sessions de l'admin (id 1) : interdit
	if w := do(s, http.MethodPost, "/api/v1/users/1/revoke-sessions", userTok, ""); w.Code != http.StatusForbidden {
		t.Fatalf("revocation d'autrui -> %d, want 403", w.Code)
	}
	// bob revoquant les siennes : autorise
	if w := do(s, http.MethodPost, "/api/v1/users/2/revoke-sessions", userTok, ""); w.Code == http.StatusForbidden {
		t.Fatal("un utilisateur doit pouvoir revoquer ses propres sessions")
	}
}

func TestRevokeEndpointRejectsAnonymous(t *testing.T) {
	s, _ := testServer(t)
	if w := do(s, http.MethodPost, "/api/v1/users/1/revoke-sessions", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonyme -> %d, want 401", w.Code)
	}
}

// Un serveur sans AuthService ne peut pas verifier les revocations : il doit
// refuser de demarrer plutot que de sauter le controle en silence.
func TestServerRefusesToStartWithoutAuthService(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewServer sans Auth devait paniquer au demarrage")
		}
	}()
	NewServer(Options{Addr: "127.0.0.1:0", JWTSecret: "x", TokenTTL: 1}, Services{}, nil, nil)
}
