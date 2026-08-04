//go:build integration

// Tests d'integration du moteur restic contre un VRAI depot et une VRAIE
// image restic.
//
// Tout le reste de la suite exerce le moteur a travers des doubles : ils
// verifient que SDB construit les bonnes commandes, jamais que restic les
// comprend. Une montee de version de restic peut donc changer le format de
// sa sortie --json, la semantique d'un code de sortie ou le nom d'un flag
// sans qu'aucun test ne bronche -- et la panne n'apparaitrait qu'en
// production, au moment de restaurer.
//
// Exclus du `go test ./...` par le tag de build : ils demandent Docker et
// tirent une image. La CI les lance dans un job dedie.
//
//	go test -tags=integration ./internal/infra/restic/...
package restic

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/standalone-docker-backup/sdb/internal/domain"
	dockerinfra "github.com/standalone-docker-backup/sdb/internal/infra/docker"
)

// Meme image que le defaut de production (cf. config.Docker.WorkerImage) :
// tester une autre version ne prouverait rien sur celle qu'on expedie.
const testResticImage = "restic/restic:0.18.0"

const helperImage = "alpine:3"

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newRuntime : runtime Docker reel, ou skip si le demon est injoignable.
func newRuntime(t *testing.T) *dockerinfra.Runtime {
	t.Helper()
	rt, err := dockerinfra.New(dockerinfra.Options{}, discardLogger())
	if err != nil {
		t.Skipf("docker indisponible : %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := rt.Ping(ctx); err != nil {
		t.Skipf("demon docker injoignable : %v", err)
	}
	t.Cleanup(func() { rt.Close() })
	return rt
}

func newDockerClient(t *testing.T) *dockerclient.Client {
	t.Helper()
	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		t.Skipf("client docker indisponible : %v", err)
	}
	t.Cleanup(func() { cli.Close() })
	return cli
}

// runInVolume : execute un script shell avec le volume monte sur /v, et
// retourne sa sortie. Sert a peupler puis relire les volumes de test.
func runInVolume(t *testing.T, cli *dockerclient.Client, volume, script string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	created, err := cli.ContainerCreate(ctx,
		&container.Config{Image: helperImage, Cmd: []string{"sh", "-c", script}},
		&container.HostConfig{Mounts: []mount.Mount{{
			Type: mount.TypeVolume, Source: volume, Target: "/v",
		}}}, nil, nil, "")
	if err != nil {
		t.Fatalf("creation du conteneur d'appui : %v", err)
	}
	defer cli.ContainerRemove(context.Background(), created.ID, container.RemoveOptions{Force: true})

	if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		t.Fatalf("demarrage du conteneur d'appui : %v", err)
	}
	waitCh, errCh := cli.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		t.Fatalf("attente du conteneur d'appui : %v", err)
	case <-waitCh:
	}

	logs, err := cli.ContainerLogs(ctx, created.ID, container.LogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		t.Fatalf("lecture des logs : %v", err)
	}
	defer logs.Close()
	var out, errBuf strings.Builder
	if _, err := stdcopy.StdCopy(&out, &errBuf, logs); err != nil {
		t.Fatalf("demultiplexage : %v", err)
	}
	return strings.TrimSpace(out.String())
}

func removeVolume(t *testing.T, cli *dockerclient.Client, name string) {
	t.Helper()
	_ = cli.VolumeRemove(context.Background(), name, true)
}

// testStorageReal : depot local sur un chemin hote jetable.
func testStorageReal(t *testing.T) *domain.StorageConfig {
	t.Helper()
	// chemin dans le contexte du DEMON, pas du processus de test : les
	// workers le montent en bind, avec CreateMountpoint
	path := fmt.Sprintf("/tmp/sdb-it-repo-%d", time.Now().UnixNano())
	return &domain.StorageConfig{
		ID: 1, Name: "integration", Type: domain.StorageLocal,
		Endpoint: path, ResticPassword: "integration-test-password",
	}
}

// drain : consomme les evenements pour ne pas bloquer le moteur, et les
// retourne.
func drain(events chan domain.ProgressEvent, done chan<- []domain.ProgressEvent) {
	var got []domain.ProgressEvent
	for ev := range events {
		got = append(got, ev)
	}
	done <- got
}

// Le cycle complet contre un vrai restic : init, sauvegarde, listing,
// restauration, verification. C'est ce qui manquait entierement.
func TestIntegrationFullBackupRestoreCycle(t *testing.T) {
	rt := newRuntime(t)
	cli := newDockerClient(t)
	engine := New(rt, testResticImage, WithReadDataSubset("100%"))
	storage := testStorageReal(t)
	ctx := context.Background()

	srcVol := fmt.Sprintf("sdb-it-src-%d", time.Now().UnixNano())
	dstVol := fmt.Sprintf("sdb-it-dst-%d", time.Now().UnixNano())
	t.Cleanup(func() { removeVolume(t, cli, srcVol); removeVolume(t, cli, dstVol) })

	const marker = "INTEGRATION-PAYLOAD-42"
	runInVolume(t, cli, srcVol, "mkdir -p /v/sub && echo '"+marker+"' > /v/marker.txt && echo nested > /v/sub/nested.txt")

	// --- init du depot ---
	if err := engine.EnsureRepository(ctx, storage); err != nil {
		t.Fatalf("EnsureRepository() : %v", err)
	}
	// idempotence : un second appel ne doit pas echouer ni reinitialiser
	if err := engine.EnsureRepository(ctx, storage); err != nil {
		t.Fatalf("EnsureRepository() au second appel : %v", err)
	}

	// --- sauvegarde ---
	events := make(chan domain.ProgressEvent, 256)
	done := make(chan []domain.ProgressEvent, 1)
	go drain(events, done)
	summary, err := engine.Backup(ctx, storage, 42,
		[]domain.Mount{{Type: domain.MountVolume, Name: srcVol}},
		[]string{"container:integration"}, events)
	close(events)
	evs := <-done
	if err != nil {
		t.Fatalf("Backup() : %v", err)
	}
	// c'est ici que se casserait un changement de format --json
	if summary == nil || summary.SnapshotID == "" {
		t.Fatalf("resume vide : le format --json de restic a probablement change (%+v)", summary)
	}
	if summary.BytesProcessed == 0 {
		t.Fatalf("BytesProcessed = 0 alors que la source contient des donnees : parsing du resume casse")
	}
	if len(evs) == 0 {
		t.Fatal("aucun evenement de progression : le decodage de la sortie restic ne produit rien")
	}

	// --- listing ---
	snaps, err := engine.Snapshots(ctx, storage, []string{"container:integration"})
	if err != nil {
		t.Fatalf("Snapshots() : %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("%d snapshot(s), want 1 — parsing de `restic snapshots --json` casse ?", len(snaps))
	}
	if snaps[0].ID != summary.SnapshotID {
		t.Fatalf("id du snapshot = %q, want %q", snaps[0].ID, summary.SnapshotID)
	}
	if len(snaps[0].Paths) == 0 || !strings.HasPrefix(snaps[0].Paths[0], dataMountRoot) {
		t.Fatalf("chemins archives inattendus : %v", snaps[0].Paths)
	}

	// --- restauration vers un volume NEUF (le clonage) ---
	rEvents := make(chan domain.ProgressEvent, 256)
	rDone := make(chan []domain.ProgressEvent, 1)
	go drain(rEvents, rDone)
	err = engine.Restore(ctx, storage, domain.RestoreSpec{
		SnapshotID: summary.SnapshotID, SourceVolume: srcVol, TargetVolume: dstVol,
	}, rEvents)
	close(rEvents)
	<-rDone
	if err != nil {
		t.Fatalf("Restore() : %v", err)
	}

	// LA verification qui compte : les donnees sont-elles vraiment la ?
	got := runInVolume(t, cli, dstVol, "cat /v/marker.txt")
	if got != marker {
		t.Fatalf("contenu restaure = %q, want %q", got, marker)
	}
	if nested := runInVolume(t, cli, dstVol, "cat /v/sub/nested.txt"); nested != "nested" {
		t.Fatalf("sous-repertoire non restaure : %q", nested)
	}
}

// --verify est ce qui transforme une restauration en PREUVE de
// restaurabilite. Si restic renommait ou retirait ce flag, la verification
// automatique deviendrait silencieusement une simple restauration.
func TestIntegrationRestoreWithVerifyIsAccepted(t *testing.T) {
	rt := newRuntime(t)
	cli := newDockerClient(t)
	engine := New(rt, testResticImage)
	storage := testStorageReal(t)
	ctx := context.Background()

	srcVol := fmt.Sprintf("sdb-it-vsrc-%d", time.Now().UnixNano())
	dstVol := domain.VerifyVolumePrefix + fmt.Sprintf("it%d", time.Now().UnixNano())
	t.Cleanup(func() { removeVolume(t, cli, srcVol); removeVolume(t, cli, dstVol) })
	runInVolume(t, cli, srcVol, "echo verified > /v/f.txt")

	if err := engine.EnsureRepository(ctx, storage); err != nil {
		t.Fatalf("EnsureRepository() : %v", err)
	}
	events := make(chan domain.ProgressEvent, 128)
	done := make(chan []domain.ProgressEvent, 1)
	go drain(events, done)
	summary, err := engine.Backup(ctx, storage, 1,
		[]domain.Mount{{Type: domain.MountVolume, Name: srcVol}}, nil, events)
	close(events)
	<-done
	if err != nil {
		t.Fatalf("Backup() : %v", err)
	}

	rEvents := make(chan domain.ProgressEvent, 128)
	rDone := make(chan []domain.ProgressEvent, 1)
	go drain(rEvents, rDone)
	err = engine.Restore(ctx, storage, domain.RestoreSpec{
		SnapshotID: summary.SnapshotID, SourceVolume: srcVol, TargetVolume: dstVol, Verify: true,
	}, rEvents)
	close(rEvents)
	<-rDone
	if err != nil {
		t.Fatalf("Restore(Verify) : %v — restic a-t-il change le flag --verify ?", err)
	}
	if got := runInVolume(t, cli, dstVol, "cat /v/f.txt"); got != "verified" {
		t.Fatalf("contenu = %q, want verified", got)
	}
}

// --read-data-subset : sans lui, check ne valide que la structure. Un
// changement de syntaxe rendrait le controle d'integrite silencieusement
// superficiel.
func TestIntegrationCheckWithReadDataSubset(t *testing.T) {
	rt := newRuntime(t)
	cli := newDockerClient(t)
	storage := testStorageReal(t)
	ctx := context.Background()

	srcVol := fmt.Sprintf("sdb-it-csrc-%d", time.Now().UnixNano())
	t.Cleanup(func() { removeVolume(t, cli, srcVol) })
	runInVolume(t, cli, srcVol, "echo data > /v/f.txt")

	engine := New(rt, testResticImage, WithReadDataSubset("100%"))
	if err := engine.EnsureRepository(ctx, storage); err != nil {
		t.Fatalf("EnsureRepository() : %v", err)
	}
	events := make(chan domain.ProgressEvent, 128)
	done := make(chan []domain.ProgressEvent, 1)
	go drain(events, done)
	if _, err := engine.Backup(ctx, storage, 1,
		[]domain.Mount{{Type: domain.MountVolume, Name: srcVol}}, nil, events); err != nil {
		close(events)
		<-done
		t.Fatalf("Backup() : %v", err)
	}
	close(events)
	<-done

	if err := engine.Check(ctx, storage); err != nil {
		t.Fatalf("Check(--read-data-subset=100%%) : %v — syntaxe du flag changee ?", err)
	}
}

// La retention supprime reellement, et le depot reste coherent apres.
func TestIntegrationForgetAppliesRetention(t *testing.T) {
	rt := newRuntime(t)
	cli := newDockerClient(t)
	engine := New(rt, testResticImage)
	storage := testStorageReal(t)
	ctx := context.Background()

	srcVol := fmt.Sprintf("sdb-it-fsrc-%d", time.Now().UnixNano())
	t.Cleanup(func() { removeVolume(t, cli, srcVol) })

	if err := engine.EnsureRepository(ctx, storage); err != nil {
		t.Fatalf("EnsureRepository() : %v", err)
	}
	// trois snapshots au contenu distinct : restic dedupliquerait sinon
	for i := 0; i < 3; i++ {
		runInVolume(t, cli, srcVol, fmt.Sprintf("echo run-%d > /v/f.txt", i))
		events := make(chan domain.ProgressEvent, 128)
		done := make(chan []domain.ProgressEvent, 1)
		go drain(events, done)
		_, err := engine.Backup(ctx, storage, int64(i),
			[]domain.Mount{{Type: domain.MountVolume, Name: srcVol}}, nil, events)
		close(events)
		<-done
		if err != nil {
			t.Fatalf("Backup() #%d : %v", i, err)
		}
	}
	if snaps, _ := engine.Snapshots(ctx, storage, nil); len(snaps) != 3 {
		t.Fatalf("%d snapshots avant retention, want 3", len(snaps))
	}

	if err := engine.Forget(ctx, storage, domain.RetentionPolicy{KeepLast: 1, Prune: true}); err != nil {
		t.Fatalf("Forget() : %v", err)
	}
	snaps, err := engine.Snapshots(ctx, storage, nil)
	if err != nil {
		t.Fatalf("Snapshots() apres Forget : %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("%d snapshots apres keep-last=1, want 1", len(snaps))
	}
	if err := engine.Check(ctx, storage); err != nil {
		t.Fatalf("depot incoherent apres prune : %v", err)
	}
}

// La sonde de cible contre un VRAI restic.
//
// Elle enchaine quatre sous-commandes dont deux qu'aucun autre test
// d'integration n'exerce dans cet ordre (`forget <id> --prune` sur un depot a
// un seul snapshot, `snapshots --json --tag`). Contre des doubles, on ne
// verifie que la forme des commandes : si restic renommait un flag ou
// changeait la semantique d'un code de sortie, la sonde annoncerait
// tranquillement « cible invalide » sur une cible parfaitement saine — ce qui
// est pire que pas de sonde, puisqu'un exploitant croirait ses identifiants
// mauvais et irait en fabriquer d'autres.
func TestIntegrationProbeExercisesTheFourRightsForReal(t *testing.T) {
	rt := newRuntime(t)
	engine := New(rt, testResticImage)
	target := testStorageReal(t)
	ctx := context.Background()

	probe, err := engine.TestTarget(ctx, target, nil)
	if err != nil {
		t.Fatalf("TestTarget() : %v", err)
	}
	if !probe.OK() {
		t.Fatalf("sonde en echec sur une cible saine, etape %q : %+v", probe.FailedStep(), probe.Steps)
	}

	want := []string{domain.ProbeInit, domain.ProbeWrite, domain.ProbeRead, domain.ProbeDelete}
	if len(probe.Steps) != len(want) {
		t.Fatalf("%d etapes, want %d : %+v", len(probe.Steps), len(want), probe.Steps)
	}
	for i, name := range want {
		if probe.Steps[i].Name != name {
			t.Fatalf("etape %d = %q, want %q", i, probe.Steps[i].Name, name)
		}
	}

	// Le depot de sonde est un SOUS-CHEMIN : la cible demandee ne doit pas
	// avoir ete initialisee, sinon la creation qui suit trouverait un depot
	// deja la et n'aurait plus rien a prouver.
	if probe.Residue == target.Endpoint {
		t.Fatalf("la sonde a travaille dans la cible demandee (%q)", target.Endpoint)
	}
	if err := engine.Check(ctx, target); err == nil {
		t.Fatalf("la cible %q est un depot restic apres la sonde : elle aurait du rester vierge", target.Endpoint)
	}

	// Et le depot de sonde, lui, doit etre reste coherent apres la purge :
	// un `forget --prune` qui casse le depot ferait passer une cible saine
	// pour saine par accident.
	residue := *target
	residue.Endpoint = probe.Residue
	if err := engine.Check(ctx, &residue); err != nil {
		t.Fatalf("depot de sonde incoherent apres forget --prune : %v", err)
	}
}

// Un snapshot inexistant doit remonter une erreur, pas reussir en silence.
func TestIntegrationRestoreUnknownSnapshotFails(t *testing.T) {
	rt := newRuntime(t)
	engine := New(rt, testResticImage)
	storage := testStorageReal(t)
	ctx := context.Background()

	if err := engine.EnsureRepository(ctx, storage); err != nil {
		t.Fatalf("EnsureRepository() : %v", err)
	}
	events := make(chan domain.ProgressEvent, 64)
	done := make(chan []domain.ProgressEvent, 1)
	go drain(events, done)
	err := engine.Restore(ctx, storage, domain.RestoreSpec{
		SnapshotID:   "0000000000000000000000000000000000000000000000000000000000000000",
		TargetVolume: "sdb-it-never-created",
	}, events)
	close(events)
	<-done
	if err == nil {
		t.Fatal("la restauration d'un snapshot inexistant doit echouer")
	}
}

// La copie secondaire ne vaut que si elle est restaurable SEULE. Ce test
// sauvegarde dans un premier depot, copie vers un second, puis restaure
// EXCLUSIVEMENT depuis le second et compare les octets : c'est la seule facon
// de prouver que la regle 3-2-1 est reellement satisfaite, et pas seulement
// qu'une commande `copy` est sortie avec le code 0.
//
// Les deux depots ont des mots de passe DIFFERENTS : restic re-encrypte a la
// copie, et un test qui partagerait le mot de passe ne verrait pas une
// regression sur --from-password-file.
func TestIntegrationSecondaryCopyIsRestorableOnItsOwn(t *testing.T) {
	rt := newRuntime(t)
	cli := newDockerClient(t)
	engine := New(rt, testResticImage)
	ctx := context.Background()

	primary := testStorageReal(t)
	primary.Name = "primary"
	secondary := testStorageReal(t)
	secondary.ID, secondary.Name = 2, "offsite"
	// suffixe explicite : l'horloge Windows n'a pas la resolution suffisante
	// pour garantir deux chemins distincts en deux appels consecutifs
	secondary.Endpoint = primary.Endpoint + "-copy"
	secondary.ResticPassword = "integration-secondary-password"
	secondary.CopyOf = primary.ID

	srcVol := fmt.Sprintf("sdb-it-copysrc-%d", time.Now().UnixNano())
	dstVol := fmt.Sprintf("sdb-it-copydst-%d", time.Now().UnixNano())
	t.Cleanup(func() { removeVolume(t, cli, srcVol); removeVolume(t, cli, dstVol) })

	const marker = "SECOND-COPY-PAYLOAD-321"
	runInVolume(t, cli, srcVol, "echo '"+marker+"' > /v/marker.txt")

	if err := engine.EnsureRepository(ctx, primary); err != nil {
		t.Fatalf("EnsureRepository(primary) : %v", err)
	}
	events := make(chan domain.ProgressEvent, 256)
	done := make(chan []domain.ProgressEvent, 1)
	go drain(events, done)
	summary, err := engine.Backup(ctx, primary, 1,
		[]domain.Mount{{Type: domain.MountVolume, Name: srcVol}}, []string{"container:copy-it"}, events)
	close(events)
	<-done
	if err != nil {
		t.Fatalf("Backup() : %v", err)
	}

	// --- init du depot secondaire depuis sa source, puis copie ---
	if err := engine.EnsureCopyTarget(ctx, secondary, primary); err != nil {
		t.Fatalf("EnsureCopyTarget() : %v — --copy-chunker-params ou --from-repo ont-ils change ?", err)
	}
	cEvents := make(chan domain.ProgressEvent, 256)
	cDone := make(chan []domain.ProgressEvent, 1)
	go drain(cEvents, cDone)
	err = engine.Copy(ctx, secondary, primary, []string{summary.SnapshotID}, cEvents)
	close(cEvents)
	<-cDone
	if err != nil {
		t.Fatalf("Copy() : %v", err)
	}

	copied, err := engine.Snapshots(ctx, secondary, nil)
	if err != nil {
		t.Fatalf("Snapshots(secondary) : %v", err)
	}
	if len(copied) != 1 {
		t.Fatalf("%d snapshot(s) dans la copie, want 1", len(copied))
	}
	// la re-encryption DOIT changer l'identifiant : c'est ce qui interdit de
	// suivre la replication par identifiant
	if copied[0].ID == summary.SnapshotID {
		t.Fatalf("le snapshot copie porte le meme id que l'original (%s) : hypothese de re-encryption fausse", copied[0].ID)
	}

	// --- restauration depuis la COPIE seule ---
	rEvents := make(chan domain.ProgressEvent, 256)
	rDone := make(chan []domain.ProgressEvent, 1)
	go drain(rEvents, rDone)
	err = engine.Restore(ctx, secondary, domain.RestoreSpec{
		SnapshotID: copied[0].ID, SourceVolume: srcVol, TargetVolume: dstVol, Verify: true,
	}, rEvents)
	close(rEvents)
	<-rDone
	if err != nil {
		t.Fatalf("Restore(depuis la copie) : %v", err)
	}
	if got := runInVolume(t, cli, dstVol, "cat /v/marker.txt"); got != marker {
		t.Fatalf("contenu restaure depuis la copie = %q, want %q", got, marker)
	}
}

// Une passe de reconciliation tourne toutes les quelques heures : si elle
// recopiait a chaque fois ce qui est deja la, elle re-televerserait le depot
// entier indefiniment.
func TestIntegrationCopySkipsSnapshotsAlreadyReplicated(t *testing.T) {
	rt := newRuntime(t)
	cli := newDockerClient(t)
	engine := New(rt, testResticImage)
	ctx := context.Background()

	primary := testStorageReal(t)
	secondary := testStorageReal(t)
	secondary.ID, secondary.Name = 2, "offsite"
	secondary.Endpoint = primary.Endpoint + "-copy"
	secondary.ResticPassword = "integration-secondary-password"

	srcVol := fmt.Sprintf("sdb-it-idem-%d", time.Now().UnixNano())
	t.Cleanup(func() { removeVolume(t, cli, srcVol) })
	runInVolume(t, cli, srcVol, "echo idempotent > /v/f.txt")

	if err := engine.EnsureRepository(ctx, primary); err != nil {
		t.Fatalf("EnsureRepository() : %v", err)
	}
	events := make(chan domain.ProgressEvent, 128)
	done := make(chan []domain.ProgressEvent, 1)
	go drain(events, done)
	if _, err := engine.Backup(ctx, primary, 1,
		[]domain.Mount{{Type: domain.MountVolume, Name: srcVol}}, nil, events); err != nil {
		close(events)
		<-done
		t.Fatalf("Backup() : %v", err)
	}
	close(events)
	<-done

	if err := engine.EnsureCopyTarget(ctx, secondary, primary); err != nil {
		t.Fatalf("EnsureCopyTarget() : %v", err)
	}
	for i := 0; i < 2; i++ {
		cEvents := make(chan domain.ProgressEvent, 128)
		cDone := make(chan []domain.ProgressEvent, 1)
		go drain(cEvents, cDone)
		err := engine.Copy(ctx, secondary, primary, nil, cEvents)
		close(cEvents)
		<-cDone
		if err != nil {
			t.Fatalf("Copy() #%d : %v", i, err)
		}
	}

	copied, err := engine.Snapshots(ctx, secondary, nil)
	if err != nil {
		t.Fatalf("Snapshots(secondary) : %v", err)
	}
	if len(copied) != 1 {
		t.Fatalf("%d snapshots apres deux copies, want 1 — restic ne saute plus ceux deja copies", len(copied))
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
