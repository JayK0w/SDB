// Package restic : implémente SnapshotEngine en pilotant le binaire restic
// dans des workers éphémères — les volumes cibles y sont montés en lecture
// seule, le processus SDB ne touche jamais les données. L'image worker
// doit avoir restic comme entrypoint (restic/restic officielle).
package restic

import (
	"fmt"
	"sort"
	"strings"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

const (
	repoMountPath = "/sdb/repo" // dépôt local monté ici dans le worker
	dataMountRoot = "/sdb/data" // préfixe des montages sauvegardés (backup ET restore)
)

// pseudo-credentials transformés en fichiers dans le worker (leurs
// consommateurs attendent des fichiers, pas des variables)
const (
	credSSHKey     = "SSH_PRIVATE_KEY"
	credGoogleJSON = "GOOGLE_CREDENTIALS_JSON"
)

const (
	sshKeyPath  = "/sdb/secrets/ssh_key"
	gcsJSONPath = "/sdb/secrets/gcs.json"
)

// repoContext : tout ce qu'il faut pour pointer un worker vers un dépôt.
type repoContext struct {
	env     []string
	mounts  []domain.Mount
	files   map[string][]byte
	opts    []string // flags restic additionnels (-o sftp.command=...)
	network string
}

func repositoryFor(storage *domain.StorageConfig) (*repoContext, error) {
	rc := &repoContext{files: map[string][]byte{}}
	switch storage.Type {
	case domain.StorageLocal:
		if !strings.HasPrefix(storage.Endpoint, "/") {
			return nil, fmt.Errorf("%w: local storage endpoint must be an absolute host path", domain.ErrInvalidInput)
		}
		rc.env = append(rc.env, "RESTIC_REPOSITORY="+repoMountPath)
		rc.mounts = append(rc.mounts, domain.Mount{
			Type:        domain.MountBind,
			Source:      storage.Endpoint,
			Destination: repoMountPath,
			ReadOnly:    false,
		})
		// sauvegarde locale = aucun réseau nécessaire : worker isolé
		rc.network = "none"
	case domain.StorageS3:
		rc.env = append(rc.env, "RESTIC_REPOSITORY="+ensureScheme(storage.Endpoint, "s3:"))
	case domain.StorageSFTP:
		rc.env = append(rc.env, "RESTIC_REPOSITORY="+ensureScheme(storage.Endpoint, "sftp:"))
		if err := configureSFTP(rc, storage); err != nil {
			return nil, err
		}
	case domain.StorageREST:
		rc.env = append(rc.env, "RESTIC_REPOSITORY="+ensureScheme(storage.Endpoint, "rest:"))
	case domain.StorageB2:
		rc.env = append(rc.env, "RESTIC_REPOSITORY="+ensureScheme(storage.Endpoint, "b2:"))
	case domain.StorageAzure:
		rc.env = append(rc.env, "RESTIC_REPOSITORY="+ensureScheme(storage.Endpoint, "azure:"))
	case domain.StorageGCS:
		rc.env = append(rc.env, "RESTIC_REPOSITORY="+ensureScheme(storage.Endpoint, "gs:"))
	default:
		return nil, fmt.Errorf("%w: unsupported storage type %q", domain.ErrInvalidInput, storage.Type)
	}

	rc.env = append(rc.env, "RESTIC_PASSWORD="+storage.ResticPassword)
	keys := make([]string, 0, len(storage.Credentials))
	for k := range storage.Credentials {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch k {
		case credSSHKey:
			rc.files[sshKeyPath] = []byte(storage.Credentials[k])
		case credGoogleJSON:
			rc.files[gcsJSONPath] = []byte(storage.Credentials[k])
			rc.env = append(rc.env, "GOOGLE_APPLICATION_CREDENTIALS="+gcsJSONPath)
		default:
			rc.env = append(rc.env, k+"="+storage.Credentials[k])
		}
	}
	return rc, nil
}

// configureSFTP : construit la commande ssh que restic lance pour sftp:.
// Clé privée via SSH_PRIVATE_KEY, host key épinglée au premier usage.
func configureSFTP(rc *repoContext, storage *domain.StorageConfig) error {
	if storage.Credentials[credSSHKey] == "" {
		return fmt.Errorf("%w: sftp storage requires the %s credential", domain.ErrInvalidInput, credSSHKey)
	}
	target := strings.TrimPrefix(storage.Endpoint, "sftp:")
	target = strings.TrimPrefix(target, "//")
	// "user@host:/chemin" -> "user@host"
	if i := strings.Index(target, ":"); i >= 0 {
		target = target[:i]
	}
	if target == "" {
		return fmt.Errorf("%w: sftp endpoint must look like user@host:/path", domain.ErrInvalidInput)
	}
	ssh := fmt.Sprintf("ssh -i %s -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/tmp/known_hosts", sshKeyPath)
	if port := storage.Credentials["SSH_PORT"]; port != "" {
		ssh += " -p " + port
	}
	rc.opts = append(rc.opts, "-o", fmt.Sprintf("sftp.command=%s %s -s sftp", ssh, target))
	return nil
}

func ensureScheme(endpoint, scheme string) string {
	if strings.HasPrefix(endpoint, scheme) {
		return endpoint
	}
	return scheme + endpoint
}

// mountName : identifiant stable et sûr pour un montage (nom du volume,
// sinon chemin nettoyé).
func mountName(m domain.Mount) string {
	name := m.Name
	if name == "" {
		name = strings.Trim(m.Destination, "/")
	}
	if name == "" {
		name = strings.Trim(m.Source, "/")
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		out = "data"
	}
	return out
}

// dataMounts : projette les montages du conteneur source dans le worker
// sous /sdb/data/<nom>, en lecture seule. Retourne aussi les chemins que
// restic doit sauvegarder.
func dataMounts(mounts []domain.Mount) ([]domain.Mount, []string, error) {
	if len(mounts) == 0 {
		return nil, nil, fmt.Errorf("%w: no mounts to back up", domain.ErrInvalidInput)
	}
	seen := map[string]int{}
	workerMounts := make([]domain.Mount, 0, len(mounts))
	paths := make([]string, 0, len(mounts))
	for _, m := range mounts {
		name := mountName(m)
		seen[name]++
		if n := seen[name]; n > 1 {
			name = fmt.Sprintf("%s-%d", name, n)
		}
		dest := dataMountRoot + "/" + name
		workerMounts = append(workerMounts, domain.Mount{
			Type:        m.Type,
			Name:        m.Name,
			Source:      m.Source,
			Destination: dest,
			ReadOnly:    true,
		})
		paths = append(paths, dest)
	}
	return workerMounts, paths, nil
}
