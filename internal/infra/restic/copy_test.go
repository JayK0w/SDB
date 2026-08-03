package restic

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// recordingRuntime : retient TOUTES les WorkerSpec et rend les codes de sortie
// programmés. EnsureCopyTarget en lance deux (sonde puis init) : ne garder que
// la dernière masquerait la sonde.
type recordingRuntime struct {
	captureRuntime
	specs []domain.WorkerSpec
	exits []int
}

func (r *recordingRuntime) RunWorker(_ context.Context, spec domain.WorkerSpec, _, _ io.Writer) (int, error) {
	r.specs = append(r.specs, spec)
	if len(r.exits) >= len(r.specs) {
		return r.exits[len(r.specs)-1], nil
	}
	return 0, nil
}

func (r *recordingRuntime) last() domain.WorkerSpec {
	return r.specs[len(r.specs)-1]
}

func env(spec domain.WorkerSpec, key string) (string, bool) {
	for _, kv := range spec.Env {
		if k, v, _ := strings.Cut(kv, "="); k == key {
			return v, true
		}
	}
	return "", false
}

func flagValue(cmd []string, flag string) (string, bool) {
	for i, arg := range cmd {
		if arg == flag && i+1 < len(cmd) {
			return cmd[i+1], true
		}
	}
	return "", false
}

func hasArg(cmd []string, want string) bool {
	for _, arg := range cmd {
		if arg == want {
			return true
		}
	}
	return false
}

func localStorage(id int64, name, path, password string) *domain.StorageConfig {
	return &domain.StorageConfig{
		ID: id, Name: name, Type: domain.StorageLocal,
		Endpoint: path, ResticPassword: password,
	}
}

// Deux dépôts dans UN worker : chacun doit garder son point de montage, sa
// variable de dépôt et son mot de passe. S'ils se partageaient un chemin ou
// une variable, la copie s'écrirait dans le dépôt source — le scénario que
// toute cette fonctionnalité doit rendre impossible.
func TestCopyKeepsSourceAndTargetSeparate(t *testing.T) {
	rt := &recordingRuntime{}
	e := New(rt, "restic/restic:latest")
	src := localStorage(1, "primary", "/srv/primary", "pw-source")
	dst := localStorage(2, "offsite", "/srv/offsite", "pw-target")
	dst.CopyOf = src.ID

	if err := e.Copy(context.Background(), dst, src, nil, nil); err != nil {
		t.Fatalf("Copy() error: %v", err)
	}
	spec := rt.last()

	if got, _ := env(spec, "RESTIC_REPOSITORY"); got != repoMountPath {
		t.Fatalf("RESTIC_REPOSITORY = %q, want %q", got, repoMountPath)
	}
	if got, _ := env(spec, "RESTIC_FROM_REPOSITORY"); got != srcRepoMountPath {
		t.Fatalf("RESTIC_FROM_REPOSITORY = %q, want %q", got, srcRepoMountPath)
	}
	if got, _ := env(spec, "RESTIC_PASSWORD"); got != "pw-target" {
		t.Fatalf("RESTIC_PASSWORD = %q, want the TARGET password", got)
	}

	byDest := map[string]domain.Mount{}
	for _, m := range spec.Mounts {
		byDest[m.Destination] = m
	}
	if m := byDest[repoMountPath]; m.Source != "/srv/offsite" {
		t.Fatalf("%s is bound to %q, want the copy target /srv/offsite", repoMountPath, m.Source)
	}
	if m := byDest[srcRepoMountPath]; m.Source != "/srv/primary" {
		t.Fatalf("%s is bound to %q, want the source /srv/primary", srcRepoMountPath, m.Source)
	}
	if spec.NetworkMode != "none" {
		t.Fatalf("network = %q, want none: a local-to-local copy needs no network", spec.NetworkMode)
	}
}

// restic n'offre aucune variable d'environnement pour le mot de passe du dépôt
// SOURCE : il passe par un fichier. Le vérifier ici évite qu'une refonte le
// remette silencieusement dans l'environnement du worker.
func TestCopySourcePasswordTravelsInAFileNotTheEnvironment(t *testing.T) {
	rt := &recordingRuntime{}
	e := New(rt, "restic/restic:latest")
	src := localStorage(1, "primary", "/srv/primary", "pw-source")
	dst := localStorage(2, "offsite", "/srv/offsite", "pw-target")

	if err := e.Copy(context.Background(), dst, src, nil, nil); err != nil {
		t.Fatalf("Copy() error: %v", err)
	}
	spec := rt.last()

	path, ok := flagValue(spec.Cmd, "--from-password-file")
	if !ok {
		t.Fatalf("no --from-password-file in %v", spec.Cmd)
	}
	if got := string(spec.Files[path]); got != "pw-source" {
		t.Fatalf("%s contains %q, want the SOURCE password", path, got)
	}
	for _, kv := range spec.Env {
		if strings.Contains(kv, "pw-source") {
			t.Fatalf("source password leaked into the environment: %s", strings.SplitN(kv, "=", 2)[0])
		}
	}
}

// Une copie vers un dépôt distant a besoin du réseau ; le couper la ferait
// échouer à chaque passe.
func TestCopyKeepsNetworkWhenOneSideIsRemote(t *testing.T) {
	rt := &recordingRuntime{}
	e := New(rt, "restic/restic:latest")
	src := localStorage(1, "primary", "/srv/primary", "pw-source")
	dst := &domain.StorageConfig{
		ID: 2, Name: "offsite", Type: domain.StorageS3,
		Endpoint: "s3.example.com/bucket", ResticPassword: "pw-target",
		Credentials: map[string]string{"AWS_ACCESS_KEY_ID": "k", "AWS_SECRET_ACCESS_KEY": "s"},
	}

	if err := e.Copy(context.Background(), dst, src, nil, nil); err != nil {
		t.Fatalf("Copy() error: %v", err)
	}
	if got := rt.last().NetworkMode; got == "none" {
		t.Fatal("a copy to a remote repository must keep network access")
	}
}

// LA limite structurelle de restic : les identifiants de backend n'ont pas de
// variante --from et sont donc partagés. Laisser passer la paire produirait
// une copie qui s'authentifie sur le mauvais compte — refuser tôt, avec le nom
// de la variable, est la seule issue honnête.
func TestCopyRefusesConflictingBackendCredentials(t *testing.T) {
	e := New(&recordingRuntime{}, "restic/restic:latest")
	src := &domain.StorageConfig{
		ID: 1, Name: "primary", Type: domain.StorageS3, Endpoint: "s3.example.com/a",
		ResticPassword: "pw-source",
		Credentials:    map[string]string{"AWS_ACCESS_KEY_ID": "key-a", "AWS_SECRET_ACCESS_KEY": "secret-a"},
	}
	dst := &domain.StorageConfig{
		ID: 2, Name: "offsite", Type: domain.StorageS3, Endpoint: "s3.example.com/b",
		ResticPassword: "pw-target",
		Credentials:    map[string]string{"AWS_ACCESS_KEY_ID": "key-b", "AWS_SECRET_ACCESS_KEY": "secret-b"},
	}

	err := e.Copy(context.Background(), dst, src, nil, nil)
	if err == nil {
		t.Fatal("copying between two S3 accounts with different credentials must be refused")
	}
	if !strings.Contains(err.Error(), "AWS_ACCESS_KEY_ID") {
		t.Fatalf("error must name the conflicting variable, got: %v", err)
	}
}

// Même compte des deux côtés (deux buckets, un seul jeu de clés) : cas
// parfaitement légitime, il ne doit pas tomber sous la règle précédente.
func TestCopyAcceptsSharedBackendCredentials(t *testing.T) {
	rt := &recordingRuntime{}
	e := New(rt, "restic/restic:latest")
	creds := map[string]string{"AWS_ACCESS_KEY_ID": "key", "AWS_SECRET_ACCESS_KEY": "secret"}
	src := &domain.StorageConfig{
		ID: 1, Name: "primary", Type: domain.StorageS3, Endpoint: "s3.example.com/a",
		ResticPassword: "pw-source", Credentials: creds,
	}
	dst := &domain.StorageConfig{
		ID: 2, Name: "offsite", Type: domain.StorageS3, Endpoint: "s3.example.com/b",
		ResticPassword: "pw-target", Credentials: creds,
	}

	if err := e.Copy(context.Background(), dst, src, nil, nil); err != nil {
		t.Fatalf("Copy() error: %v", err)
	}
	var count int
	for _, kv := range rt.last().Env {
		if strings.HasPrefix(kv, "AWS_ACCESS_KEY_ID=") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("AWS_ACCESS_KEY_ID appears %d times, want exactly 1", count)
	}
}

func TestCopyForwardsRequestedSnapshots(t *testing.T) {
	rt := &recordingRuntime{}
	e := New(rt, "restic/restic:latest")
	src := localStorage(1, "primary", "/srv/primary", "pw-source")
	dst := localStorage(2, "offsite", "/srv/offsite", "pw-target")

	if err := e.Copy(context.Background(), dst, src, []string{"snap-a", "snap-b"}, nil); err != nil {
		t.Fatalf("Copy() error: %v", err)
	}
	cmd := rt.last().Cmd
	if cmd[0] != "copy" || !hasArg(cmd, "snap-a") || !hasArg(cmd, "snap-b") {
		t.Fatalf("command = %v, want a copy of both snapshots", cmd)
	}
}

// --copy-chunker-params ne s'applique qu'À L'INITIALISATION : un dépôt créé
// sans lui garde ses paramètres pour toujours et peut stocker les données
// copiées en double.
func TestEnsureCopyTargetInitialisesFromSource(t *testing.T) {
	// sonde `cat config` en échec = dépôt absent, puis init
	rt := &recordingRuntime{exits: []int{1, 0}}
	e := New(rt, "restic/restic:latest")
	src := localStorage(1, "primary", "/srv/primary", "pw-source")
	dst := localStorage(2, "offsite", "/srv/offsite", "pw-target")

	if err := e.EnsureCopyTarget(context.Background(), dst, src); err != nil {
		t.Fatalf("EnsureCopyTarget() error: %v", err)
	}
	if len(rt.specs) != 2 {
		t.Fatalf("%d commands run, want 2 (probe then init)", len(rt.specs))
	}
	init := rt.last()
	if init.Cmd[0] != "init" || !hasArg(init.Cmd, "--copy-chunker-params") {
		t.Fatalf("init command = %v, want an init inheriting the chunker parameters", init.Cmd)
	}
	if got, _ := env(init, "RESTIC_FROM_REPOSITORY"); got != srcRepoMountPath {
		t.Fatalf("init cannot inherit anything without the source repository: %v", init.Env)
	}
}

// Ré-initialiser un dépôt qui existe déjà écraserait ses clés : la sonde doit
// suffire à s'arrêter.
func TestEnsureCopyTargetLeavesExistingRepositoryAlone(t *testing.T) {
	rt := &recordingRuntime{exits: []int{0}}
	e := New(rt, "restic/restic:latest")
	src := localStorage(1, "primary", "/srv/primary", "pw-source")
	dst := localStorage(2, "offsite", "/srv/offsite", "pw-target")

	if err := e.EnsureCopyTarget(context.Background(), dst, src); err != nil {
		t.Fatalf("EnsureCopyTarget() error: %v", err)
	}
	if len(rt.specs) != 1 {
		t.Fatalf("%d commands run, want only the probe", len(rt.specs))
	}
}
