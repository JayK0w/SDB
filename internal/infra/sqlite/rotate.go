package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// Snapshot : copie CONSISTANTE de la base vers path, via `VACUUM INTO`.
//
// Copier le fichier avec cp ne suffit pas en mode WAL : le journal vit à côté
// et une copie prise entre deux écritures peut être tronquée. VACUUM INTO
// écrit une base complète et cohérente, y compris ce qui n'est encore que dans
// le WAL. Refuse d'écraser un fichier existant (garde-fou de SQLite).
func Snapshot(ctx context.Context, db *sql.DB, path string) error {
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("writing database snapshot to %s: %w", path, err)
	}
	return nil
}

// RotateMasterKey : re-chiffre tous les secrets de stockage sous une nouvelle
// clé maître, et retourne le nombre de dépôts traités.
//
// La clé maître ne protège pas des données mais des CLÉS : les identifiants de
// backend et les mots de passe des dépôts restic. La compromettre donne accès
// à toutes les sauvegardes ; ne pas savoir la changer transforme un soupçon de
// fuite en impasse.
//
// Trois garde-fous, parce qu'une rotation ratée à mi-chemin laisserait une
// base dont une partie des secrets est illisible :
//   - tout se passe dans UNE transaction : au premier échec, rien n'a bougé ;
//   - chaque valeur ré-encryptée est relue et redéchiffrée avec la NOUVELLE
//     clé, et comparée à l'original, avant le commit — on ne se contente pas
//     de supposer que le chiffrement a marché ;
//   - une clé qui ne déchiffre pas les données existantes échoue au premier
//     enregistrement, avant toute écriture.
func RotateMasterKey(ctx context.Context, db *sql.DB, oldCipher, newCipher domain.Cipher) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx,
		`SELECT id, name, credentials_enc, restic_password_enc FROM storage_configs ORDER BY id`)
	if err != nil {
		return 0, fmt.Errorf("reading storage configs: %w", err)
	}
	type sealed struct {
		id                int64
		name              string
		creds, password   []byte
		credsPT, passwdPT []byte
	}
	var configs []sealed
	for rows.Next() {
		var s sealed
		if err := rows.Scan(&s.id, &s.name, &s.creds, &s.password); err != nil {
			rows.Close()
			return 0, err
		}
		configs = append(configs, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for i := range configs {
		s := &configs[i]
		if s.credsPT, err = oldCipher.Decrypt(s.creds); err != nil {
			return 0, fmt.Errorf("storage %q (%d): %w — is SDB_MASTER_KEY the CURRENT key?", s.name, s.id, err)
		}
		if s.passwdPT, err = oldCipher.Decrypt(s.password); err != nil {
			return 0, fmt.Errorf("storage %q (%d): %w — is SDB_MASTER_KEY the CURRENT key?", s.name, s.id, err)
		}

		creds, err := newCipher.Encrypt(s.credsPT)
		if err != nil {
			return 0, fmt.Errorf("storage %q (%d): re-encrypting credentials: %w", s.name, s.id, err)
		}
		password, err := newCipher.Encrypt(s.passwdPT)
		if err != nil {
			return 0, fmt.Errorf("storage %q (%d): re-encrypting repository password: %w", s.name, s.id, err)
		}
		if err := verifyRoundTrip(newCipher, creds, s.credsPT); err != nil {
			return 0, fmt.Errorf("storage %q (%d): credentials %w", s.name, s.id, err)
		}
		if err := verifyRoundTrip(newCipher, password, s.passwdPT); err != nil {
			return 0, fmt.Errorf("storage %q (%d): repository password %w", s.name, s.id, err)
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE storage_configs SET credentials_enc = ?, restic_password_enc = ?, updated_at = ? WHERE id = ?`,
			creds, password, now(), s.id); err != nil {
			return 0, fmt.Errorf("storage %q (%d): %w", s.name, s.id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing rotation: %w", err)
	}
	return len(configs), nil
}

// verifyRoundTrip : la valeur ré-encryptée redonne bien l'original.
func verifyRoundTrip(c domain.Cipher, ciphertext, want []byte) error {
	got, err := c.Decrypt(ciphertext)
	if err != nil {
		return fmt.Errorf("cannot be read back with the new key: %w", err)
	}
	if string(got) != string(want) {
		return fmt.Errorf("read back as different data with the new key")
	}
	return nil
}
