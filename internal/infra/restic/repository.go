// Package restic implements domain.SnapshotEngine by driving the restic
// binary inside ephemeral worker containers: the target volumes are
// attached read-only to the worker, so the SDB process itself never
// touches the data. The worker image must have restic as its entrypoint
// (e.g. the official restic/restic image).
package restic

import (
	"fmt"
	"sort"
	"strings"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

const (
	// repoMountPath is where a local repository is mounted inside workers.
	repoMountPath = "/sdb/repo"
	// dataMountRoot prefixes every backed-up mount inside workers; restores
	// reuse the same layout so snapshots restore in place.
	dataMountRoot = "/sdb/data"
)

// Pseudo-credential keys turned into files inside the worker instead of
// environment variables, because their consumers expect files.
const (
	credSSHKey     = "SSH_PRIVATE_KEY"         // sftp: private key for the ssh client
	credGoogleJSON = "GOOGLE_CREDENTIALS_JSON" // gs: service account JSON document
)

const (
	sshKeyPath  = "/sdb/secrets/ssh_key"
	gcsJSONPath = "/sdb/secrets/gcs.json"
)

// repoContext is everything needed to point a worker at a repository.
type repoContext struct {
	env     []string
	mounts  []domain.Mount
	files   map[string][]byte
	opts    []string // extra restic CLI flags (e.g. -o sftp.command=...)
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
		// A local backup needs no network at all: isolate the worker.
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

// configureSFTP wires the ssh command restic spawns for sftp: backends.
// The private key comes from the SSH_PRIVATE_KEY credential; host keys are
// pinned on first use (accept-new) inside the ephemeral worker.
func configureSFTP(rc *repoContext, storage *domain.StorageConfig) error {
	if storage.Credentials[credSSHKey] == "" {
		return fmt.Errorf("%w: sftp storage requires the %s credential", domain.ErrInvalidInput, credSSHKey)
	}
	target := strings.TrimPrefix(storage.Endpoint, "sftp:")
	target = strings.TrimPrefix(target, "//")
	// "user@host:/path" -> "user@host"
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

// mountName produces a stable, filesystem-safe identifier for a mount:
// the volume name when there is one, otherwise a sanitized path.
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

// dataMounts maps the source container's mounts into the worker under
// /sdb/data/<name>, read-only. It returns the worker mounts and the paths
// restic must back up.
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
