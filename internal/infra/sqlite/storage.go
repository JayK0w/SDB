package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// StorageRepo : les credentials et le mot de passe restic passent par le
// Cipher à chaque lecture/écriture — jamais de clair dans le fichier .db.
type StorageRepo struct {
	db     *sql.DB
	cipher domain.Cipher
}

var _ domain.StorageRepository = (*StorageRepo)(nil)

func NewStorageRepo(db *sql.DB, cipher domain.Cipher) *StorageRepo {
	return &StorageRepo{db: db, cipher: cipher}
}

const storageColumns = `id, name, type, endpoint, credentials_enc, restic_password_enc, created_at, updated_at`

func (r *StorageRepo) seal(cfg *domain.StorageConfig) (creds, password []byte, err error) {
	rawCreds, err := json.Marshal(cfg.Credentials)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding credentials: %w", err)
	}
	creds, err = r.cipher.Encrypt(rawCreds)
	if err != nil {
		return nil, nil, fmt.Errorf("encrypting credentials: %w", err)
	}
	password, err = r.cipher.Encrypt([]byte(cfg.ResticPassword))
	if err != nil {
		return nil, nil, fmt.Errorf("encrypting restic password: %w", err)
	}
	return creds, password, nil
}

func (r *StorageRepo) unseal(cfg *domain.StorageConfig, creds, password []byte) error {
	rawCreds, err := r.cipher.Decrypt(creds)
	if err != nil {
		return fmt.Errorf("decrypting credentials of storage %d: %w", cfg.ID, err)
	}
	if err := json.Unmarshal(rawCreds, &cfg.Credentials); err != nil {
		return fmt.Errorf("decoding credentials of storage %d: %w", cfg.ID, err)
	}
	rawPassword, err := r.cipher.Decrypt(password)
	if err != nil {
		return fmt.Errorf("decrypting restic password of storage %d: %w", cfg.ID, err)
	}
	cfg.ResticPassword = string(rawPassword)
	return nil
}

func (r *StorageRepo) Create(ctx context.Context, cfg *domain.StorageConfig) error {
	creds, password, err := r.seal(cfg)
	if err != nil {
		return err
	}
	if cfg.CreatedAt.IsZero() {
		cfg.CreatedAt = time.Now().UTC()
	}
	if cfg.UpdatedAt.IsZero() {
		cfg.UpdatedAt = cfg.CreatedAt
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO storage_configs (name, type, endpoint, credentials_enc, restic_password_enc, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		cfg.Name, string(cfg.Type), cfg.Endpoint, creds, password, fmtTime(cfg.CreatedAt), fmtTime(cfg.UpdatedAt))
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: storage %q", domain.ErrAlreadyExists, cfg.Name)
	}
	if err != nil {
		return fmt.Errorf("inserting storage config: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	cfg.ID = id
	return nil
}

func (r *StorageRepo) GetByID(ctx context.Context, id int64) (*domain.StorageConfig, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+storageColumns+` FROM storage_configs WHERE id = ?`, id)
	return r.scanStorage(row)
}

func (r *StorageRepo) List(ctx context.Context) ([]domain.StorageConfig, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+storageColumns+` FROM storage_configs ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing storage configs: %w", err)
	}
	defer rows.Close()

	var out []domain.StorageConfig
	for rows.Next() {
		cfg, err := r.scanStorage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *cfg)
	}
	return out, rows.Err()
}

func (r *StorageRepo) Update(ctx context.Context, cfg *domain.StorageConfig) error {
	creds, password, err := r.seal(cfg)
	if err != nil {
		return err
	}
	cfg.UpdatedAt = time.Now().UTC()
	res, err := r.db.ExecContext(ctx,
		`UPDATE storage_configs
		 SET name = ?, type = ?, endpoint = ?, credentials_enc = ?, restic_password_enc = ?, updated_at = ?
		 WHERE id = ?`,
		cfg.Name, string(cfg.Type), cfg.Endpoint, creds, password, fmtTime(cfg.UpdatedAt), cfg.ID)
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: storage %q", domain.ErrAlreadyExists, cfg.Name)
	}
	if err != nil {
		return fmt.Errorf("updating storage config %d: %w", cfg.ID, err)
	}
	return requireAffected(res, domain.ErrNotFound)
}

func (r *StorageRepo) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM storage_configs WHERE id = ?`, id)
	// FK RESTRICT : refuse si l'historique référence encore ce stockage
	if isForeignKeyViolation(err) {
		return fmt.Errorf("%w: storage %d is referenced by backup history", domain.ErrConflict, id)
	}
	if err != nil {
		return fmt.Errorf("deleting storage config %d: %w", id, err)
	}
	return requireAffected(res, domain.ErrNotFound)
}

func (r *StorageRepo) scanStorage(row rowScanner) (*domain.StorageConfig, error) {
	var cfg domain.StorageConfig
	var typ, createdAt, updatedAt string
	var creds, password []byte
	err := row.Scan(&cfg.ID, &cfg.Name, &typ, &cfg.Endpoint, &creds, &password, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning storage config: %w", err)
	}
	cfg.Type = domain.StorageType(typ)
	cfg.CreatedAt = parseTime(createdAt)
	cfg.UpdatedAt = parseTime(updatedAt)
	if err := r.unseal(&cfg, creds, password); err != nil {
		return nil, err
	}
	return &cfg, nil
}
