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
	// srcRepoMountPath : dépôt SOURCE d'une copie, monté à part — une copie
	// locale → locale ouvre deux dépôts dans le même worker.
	srcRepoMountPath = "/sdb/repo-source"
)

// pseudo-credentials transformés en fichiers dans le worker (leurs
// consommateurs attendent des fichiers, pas des variables)
const (
	credSSHKey     = "SSH_PRIVATE_KEY"
	credGoogleJSON = "GOOGLE_CREDENTIALS_JSON"
)

// repoRole : place du dépôt dans la commande. restic n'accepte qu'UN dépôt
// source (--from-*) et UN dépôt cible : chaque rôle a donc ses variables, ses
// chemins de secrets et son point de montage, sinon les deux s'écrasent.
type repoRole int

const (
	roleTarget repoRole = iota // dépôt courant (RESTIC_REPOSITORY)
	roleSource                 // dépôt source d'une copie (RESTIC_FROM_REPOSITORY)
)

func (r repoRole) repoEnv() string {
	if r == roleSource {
		return "RESTIC_FROM_REPOSITORY"
	}
	return "RESTIC_REPOSITORY"
}

func (r repoRole) mountPath() string {
	if r == roleSource {
		return srcRepoMountPath
	}
	return repoMountPath
}

func (r repoRole) secretPath(name string) string {
	if r == roleSource {
		return "/sdb/secrets/source_" + name
	}
	return "/sdb/secrets/" + name
}

// repoContext : tout ce qu'il faut pour pointer un worker vers un dépôt.
type repoContext struct {
	env     []string
	mounts  []domain.Mount
	files   map[string][]byte
	opts    []string // flags restic additionnels (-o sftp.command=...)
	network string
}

func repositoryFor(storage *domain.StorageConfig) (*repoContext, error) {
	return repositoryAs(storage, roleTarget)
}

func repositoryAs(storage *domain.StorageConfig, role repoRole) (*repoContext, error) {
	rc := &repoContext{files: map[string][]byte{}}
	repoVar := role.repoEnv()
	switch storage.Type {
	case domain.StorageLocal:
		if !strings.HasPrefix(storage.Endpoint, "/") {
			return nil, fmt.Errorf("%w: local storage endpoint must be an absolute host path", domain.ErrInvalidInput)
		}
		rc.env = append(rc.env, repoVar+"="+role.mountPath())
		rc.mounts = append(rc.mounts, domain.Mount{
			Type:        domain.MountBind,
			Source:      storage.Endpoint,
			Destination: role.mountPath(),
			ReadOnly:    false,
		})
		// sauvegarde locale = aucun réseau nécessaire : worker isolé
		rc.network = "none"
	case domain.StorageS3:
		rc.env = append(rc.env, repoVar+"="+ensureScheme(storage.Endpoint, "s3:"))
	case domain.StorageSFTP:
		rc.env = append(rc.env, repoVar+"="+ensureScheme(storage.Endpoint, "sftp:"))
		if err := configureSFTP(rc, storage, role); err != nil {
			return nil, err
		}
	case domain.StorageREST:
		rc.env = append(rc.env, repoVar+"="+ensureScheme(storage.Endpoint, "rest:"))
	case domain.StorageB2:
		rc.env = append(rc.env, repoVar+"="+ensureScheme(storage.Endpoint, "b2:"))
	case domain.StorageAzure:
		rc.env = append(rc.env, repoVar+"="+ensureScheme(storage.Endpoint, "azure:"))
	case domain.StorageGCS:
		rc.env = append(rc.env, repoVar+"="+ensureScheme(storage.Endpoint, "gs:"))
	default:
		return nil, fmt.Errorf("%w: unsupported storage type %q", domain.ErrInvalidInput, storage.Type)
	}

	if role == roleSource {
		// restic n'a pas d'équivalent de RESTIC_PASSWORD pour le dépôt source
		// (seulement --from-password-file / --from-password-command) : le mot
		// de passe passe donc par un fichier 0600 du worker, ce qui le tient
		// aussi hors de l'environnement du processus.
		path := role.secretPath("repo_password")
		rc.files[path] = []byte(storage.ResticPassword)
		rc.opts = append(rc.opts, "--from-password-file", path)
	} else {
		rc.env = append(rc.env, "RESTIC_PASSWORD="+storage.ResticPassword)
	}

	keys := make([]string, 0, len(storage.Credentials))
	for k := range storage.Credentials {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch k {
		case credSSHKey:
			rc.files[role.secretPath("ssh_key")] = []byte(storage.Credentials[k])
		case credGoogleJSON:
			path := role.secretPath("gcs.json")
			rc.files[path] = []byte(storage.Credentials[k])
			rc.env = append(rc.env, "GOOGLE_APPLICATION_CREDENTIALS="+path)
		default:
			rc.env = append(rc.env, k+"="+storage.Credentials[k])
		}
	}
	return rc, nil
}

// configureSFTP : construit la commande ssh que restic lance pour sftp:.
// Clé privée via SSH_PRIVATE_KEY, host key épinglée au premier usage.
func configureSFTP(rc *repoContext, storage *domain.StorageConfig, role repoRole) error {
	if storage.Credentials[credSSHKey] == "" {
		return fmt.Errorf("%w: sftp storage requires the %s credential", domain.ErrInvalidInput, credSSHKey)
	}
	sshKeyPath := role.secretPath("ssh_key")
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

// copyContext : un seul worker, deux dépôts — la cible en RESTIC_* et la
// source en RESTIC_FROM_*.
//
// Limite structurelle de restic : les identifiants des BACKENDS (AWS_*, B2_*,
// AZURE_*...) n'ont pas de variante --from et sont donc partagés par les deux
// dépôts. Copier entre deux comptes S3 distincts est impossible en un seul
// processus. Plutôt que de laisser la cible écraser silencieusement la source
// — ce qui donnerait « dépôt introuvable » ou, pire, une copie écrite au
// mauvais endroit — le conflit est refusé ici, avec le nom de la variable
// fautive.
func copyContext(dst, src *domain.StorageConfig) (*repoContext, error) {
	target, err := repositoryAs(dst, roleTarget)
	if err != nil {
		return nil, fmt.Errorf("copy target: %w", err)
	}
	source, err := repositoryAs(src, roleSource)
	if err != nil {
		return nil, fmt.Errorf("copy source: %w", err)
	}

	merged := &repoContext{
		env:    append([]string(nil), target.env...),
		mounts: append([]domain.Mount(nil), target.mounts...),
		files:  map[string][]byte{},
		opts:   append([]string(nil), target.opts...),
		// le réseau n'est coupé que si AUCUN des deux dépôts n'en a besoin
		network: target.network,
	}
	if merged.network != "none" || source.network != "none" {
		merged.network = ""
	}
	for path, content := range target.files {
		merged.files[path] = content
	}

	existing := map[string]string{}
	for _, kv := range target.env {
		k, v, _ := strings.Cut(kv, "=")
		existing[k] = v
	}
	for _, kv := range source.env {
		k, v, _ := strings.Cut(kv, "=")
		old, dup := existing[k]
		if dup {
			if old != v {
				return nil, fmt.Errorf(
					"%w: storages %q and %q both define %s with different values; restic shares backend credentials between a repository and its copy source",
					domain.ErrInvalidInput, dst.Name, src.Name, k)
			}
			continue
		}
		existing[k] = v
		merged.env = append(merged.env, kv)
	}

	for path, content := range source.files {
		if _, dup := merged.files[path]; dup {
			return nil, fmt.Errorf("%w: storages %q and %q need different contents at %s",
				domain.ErrInvalidInput, dst.Name, src.Name, path)
		}
		merged.files[path] = content
	}
	merged.mounts = append(merged.mounts, source.mounts...)

	// `-o clé=valeur` est global à la commande : deux valeurs pour la même clé
	// ne cohabitent pas (deux dépôts sftp distincts, typiquement).
	if err := mergeOptions(merged, source.opts, dst.Name, src.Name); err != nil {
		return nil, err
	}
	return merged, nil
}

func mergeOptions(merged *repoContext, opts []string, dstName, srcName string) error {
	keys := map[string]string{}
	collect := func(list []string) error {
		for i := 0; i+1 < len(list); i++ {
			if list[i] != "-o" {
				continue
			}
			k, v, _ := strings.Cut(list[i+1], "=")
			if old, dup := keys[k]; dup && old != v {
				return fmt.Errorf("%w: storages %q and %q both need extended option %s with different values",
					domain.ErrInvalidInput, dstName, srcName, k)
			}
			keys[k] = v
		}
		return nil
	}
	if err := collect(merged.opts); err != nil {
		return err
	}
	if err := collect(opts); err != nil {
		return err
	}
	merged.opts = append(merged.opts, opts...)
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
