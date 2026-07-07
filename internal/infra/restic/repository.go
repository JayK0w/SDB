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

// repoContext is everything needed to point a worker at a repository.
type repoContext struct {
	env     []string
	mounts  []domain.Mount
	network string
}

func repositoryFor(storage *domain.StorageConfig) (*repoContext, error) {
	rc := &repoContext{}
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
	case domain.StorageREST:
		rc.env = append(rc.env, "RESTIC_REPOSITORY="+ensureScheme(storage.Endpoint, "rest:"))
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
		rc.env = append(rc.env, k+"="+storage.Credentials[k])
	}
	return rc, nil
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
