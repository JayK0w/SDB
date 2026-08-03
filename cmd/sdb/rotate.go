package main

import (
	"context"
	"fmt"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/config"
	"github.com/standalone-docker-backup/sdb/internal/infra/crypto"
	"github.com/standalone-docker-backup/sdb/internal/infra/sqlite"
)

// rotateMasterKey : commande de maintenance `sdb rotate-master-key`.
//
// La clé maître chiffre les identifiants de backend et les mots de passe des
// dépôts restic. La croire compromise sans pouvoir en changer laisse un seul
// recours — recréer tous les dépôts — c'est-à-dire perdre l'historique.
//
// Hors ligne par choix : aucune route d'API ne déclenche cette opération. Elle
// réécrit TOUS les secrets de la base ; un compte admin compromis n'a pas à
// pouvoir la lancer, et un démon en marche garde de toute façon l'ancienne clé
// en mémoire.
func rotateMasterKey() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}
	newKey, err := config.Secret("SDB_NEW_MASTER_KEY")
	if err != nil {
		return err
	}
	switch {
	case newKey == "":
		return fmt.Errorf("SDB_NEW_MASTER_KEY (or SDB_NEW_MASTER_KEY_FILE) is required — " +
			"generate one with `openssl rand -hex 32`, and keep SDB_MASTER_KEY set to the CURRENT key")
	case len(newKey) < 32:
		return fmt.Errorf("SDB_NEW_MASTER_KEY must be at least 32 characters")
	case newKey == cfg.Auth.MasterKey:
		return fmt.Errorf("SDB_NEW_MASTER_KEY is identical to the current key — nothing to rotate")
	}

	oldCipher, err := crypto.NewAESGCM(cfg.Auth.MasterKey)
	if err != nil {
		return fmt.Errorf("current master key: %w", err)
	}
	newCipher, err := crypto.NewAESGCM(newKey)
	if err != nil {
		return fmt.Errorf("new master key: %w", err)
	}

	ctx := context.Background()
	db, err := sqlite.Open(cfg.Database.Path)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	// Filet de sécurité AVANT toute écriture. La rotation est transactionnelle,
	// mais une base copiée juste avant reste la seule issue si la nouvelle clé
	// est perdue entre le commit et le redémarrage.
	snapshot := fmt.Sprintf("%s.pre-rotation-%s", cfg.Database.Path, time.Now().UTC().Format("20060102T150405Z"))
	if err := sqlite.Snapshot(ctx, db, snapshot); err != nil {
		return err
	}
	fmt.Printf("pre-rotation snapshot written to %s\n", snapshot)

	n, err := sqlite.RotateMasterKey(ctx, db, oldCipher, newCipher)
	if err != nil {
		fmt.Printf("rotation ABORTED, database unchanged (snapshot %s can be deleted)\n", snapshot)
		return err
	}

	fmt.Printf("re-encrypted the secrets of %d storage target(s) under the new master key\n", n)
	fmt.Println("next steps:")
	fmt.Println("  1. set SDB_MASTER_KEY to the NEW key (or update the Docker secret file)")
	fmt.Println("  2. restart SDB — a running daemon still holds the old key in memory")
	fmt.Println("  3. check the logs: a failure to decrypt a storage config means the key is wrong")
	fmt.Printf("  4. DESTROY %s once SDB is confirmed healthy — it still holds every secret\n", snapshot)
	fmt.Println("     under the OLD key, which is exactly what you were rotating away from")
	return nil
}

const usage = `sdb — Standalone Docker Backup

usage:
  sdb                     start the daemon (default)
  sdb rotate-master-key   re-encrypt every stored secret under SDB_NEW_MASTER_KEY
`
