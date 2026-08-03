package domain

import "time"

type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

func (r Role) Valid() bool { return r == RoleAdmin || r == RoleUser }

// User : compte opérateur. PasswordHash = hash Argon2id, jamais le clair.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Role         Role
	// TokenVersion : génération des jetons de ce compte. Elle est inscrite
	// dans chaque JWT émis et revérifiée à chaque requête ; l'incrémenter
	// invalide instantanément TOUTES les sessions du compte.
	//
	// Un JWT est autoportant : sans ce contre-poids, un jeton volé reste
	// valable jusqu'à son expiration, et retirer son rôle admin à quelqu'un
	// — ou supprimer son compte — ne mettait pas fin à sa session. Sur un
	// système capable d'écraser des volumes de production, c'est
	// inacceptable.
	//
	// Un simple compteur suffit là où une liste de révocation devrait
	// stocker chaque jeton jusqu'à péremption.
	TokenVersion int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (u *User) IsAdmin() bool { return u.Role == RoleAdmin }
