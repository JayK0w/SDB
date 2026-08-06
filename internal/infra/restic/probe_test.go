package restic

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// probeRuntime : recordingRuntime qui sait AUSSI répondre sur stdout, ce dont
// l'étape de lecture a besoin (`snapshots --json`).
type probeRuntime struct {
	captureRuntime
	specs []domain.WorkerSpec
	// exitFor : code de sortie par sous-commande restic ; absent = 0.
	exitFor map[string]int
	// snapshotsJSON : réponse de `snapshots --json`. Vide = un snapshot.
	snapshotsJSON string
}

func (r *probeRuntime) RunWorker(_ context.Context, spec domain.WorkerSpec, stdout, _ io.Writer) (int, error) {
	r.specs = append(r.specs, spec)
	sub := ""
	if len(spec.Cmd) > 0 {
		sub = spec.Cmd[0]
	}
	if sub == "snapshots" {
		body := r.snapshotsJSON
		if body == "" {
			body = `[{"id":"deadbeefcafe","short_id":"deadbeef"}]`
		}
		if _, err := io.WriteString(stdout, body); err != nil {
			return -1, err
		}
	}
	return r.exitFor[sub], nil
}

// subcommands : la suite des sous-commandes restic lancées, dans l'ordre.
func (r *probeRuntime) subcommands() []string {
	out := make([]string, 0, len(r.specs))
	for _, s := range r.specs {
		if len(s.Cmd) > 0 {
			out = append(out, s.Cmd[0])
		}
	}
	return out
}

func newProbeRuntime() *probeRuntime {
	return &probeRuntime{exitFor: map[string]int{}}
}

func stepNames(p *domain.TargetProbe) []string {
	out := make([]string, 0, len(p.Steps))
	for _, s := range p.Steps {
		out = append(out, s.Name)
	}
	return out
}

// La sonde doit exercer la SUPPRESSION. C'est sa seule raison d'exister à côté
// de la création, qui initialise déjà le dépôt et couvre donc lister et
// écrire : une clé sans droit de suppression passe la création et ne casse
// qu'au premier retrait de verrou, des jours plus tard.
func TestProbeExercisesTheDeleteRight(t *testing.T) {
	rt := newProbeRuntime()
	e := New(rt, "restic/restic:latest")

	probe, err := e.TestTarget(context.Background(),
		localStorage(0, "offsite", "/srv/offsite", "pw"), nil)
	if err != nil {
		t.Fatalf("TestTarget() error: %v", err)
	}
	if !probe.OK() {
		t.Fatalf("probe not OK: %+v", probe.Steps)
	}

	got := rt.subcommands()
	want := []string{"init", "backup", "snapshots", "forget"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("subcommands = %v, want %v", got, want)
	}
	// `forget` sans `--prune` retire la référence mais ne supprime AUCUN
	// paquet : le droit de suppression resterait non testé.
	if !hasArg(rt.specs[3].Cmd, "--prune") {
		t.Fatalf("forget must run with --prune, got %v", rt.specs[3].Cmd)
	}

	names := strings.Join(stepNames(probe), ",")
	wantNames := strings.Join([]string{
		domain.ProbeInit, domain.ProbeWrite, domain.ProbeRead, domain.ProbeDelete,
	}, ",")
	if names != wantNames {
		t.Fatalf("steps = %s, want %s", names, wantNames)
	}
}

// Propriété de sûreté : tester une configuration qui pointe sur un dépôt DÉJÀ
// REMPLI ne doit rien pouvoir lui faire. La sonde écrit puis purge — la
// diriger sur l'emplacement demandé effacerait des sauvegardes réelles.
func TestProbeNeverTouchesTheRequestedEndpoint(t *testing.T) {
	rt := newProbeRuntime()
	e := New(rt, "restic/restic:latest")
	const endpoint = "/srv/offsite"

	probe, err := e.TestTarget(context.Background(),
		localStorage(0, "offsite", endpoint, "pw"), nil)
	if err != nil {
		t.Fatalf("TestTarget() error: %v", err)
	}

	for i, spec := range rt.specs {
		for _, m := range spec.Mounts {
			if m.Source == endpoint {
				t.Fatalf("worker %d mounts the requested endpoint %q itself", i, endpoint)
			}
			if !strings.HasPrefix(m.Source, endpoint+"/"+probeDirPrefix) {
				t.Fatalf("worker %d mount source = %q, want a probe sub-path under %q", i, m.Source, endpoint)
			}
		}
	}
	if probe.Residue == endpoint || !strings.HasPrefix(probe.Residue, endpoint+"/"+probeDirPrefix) {
		t.Fatalf("residue = %q, want a probe sub-path under %q", probe.Residue, endpoint)
	}
}

// Un échec d'init rend les étapes suivantes ininterprétables : les tenter
// produirait des erreurs en cascade qui noieraient la vraie cause.
func TestProbeStopsAtTheFirstFailure(t *testing.T) {
	rt := newProbeRuntime()
	rt.exitFor["init"] = 1
	e := New(rt, "restic/restic:latest")

	probe, err := e.TestTarget(context.Background(),
		localStorage(0, "offsite", "/srv/offsite", "pw"), nil)
	if err != nil {
		t.Fatalf("TestTarget() error: %v", err)
	}
	if probe.OK() {
		t.Fatal("probe reported OK although init failed")
	}
	if probe.FailedStep() != domain.ProbeInit {
		t.Fatalf("failed step = %q, want %q", probe.FailedStep(), domain.ProbeInit)
	}
	if len(rt.specs) != 1 {
		t.Fatalf("%d workers started after a failed init, want 1", len(rt.specs))
	}
	// Rien n'a pu être créé : annoncer un résidu enverrait l'exploitant
	// nettoyer un chemin qui n'existe pas.
	if probe.Residue != "" {
		t.Fatalf("residue = %q, want empty when init failed", probe.Residue)
	}
}

// Le cas qui justifie la fonctionnalité : tout marche SAUF la suppression.
func TestProbeReportsAMissingDeleteRight(t *testing.T) {
	rt := newProbeRuntime()
	rt.exitFor["forget"] = 1
	e := New(rt, "restic/restic:latest")

	probe, err := e.TestTarget(context.Background(),
		localStorage(0, "offsite", "/srv/offsite", "pw"), nil)
	if err != nil {
		t.Fatalf("TestTarget() error: %v", err)
	}
	if probe.FailedStep() != domain.ProbeDelete {
		t.Fatalf("failed step = %q, want %q", probe.FailedStep(), domain.ProbeDelete)
	}
	// Les trois premières doivent être rapportées comme réussies : c'est ce
	// qui dit à l'exploitant que seul le droit de suppression manque.
	for _, s := range probe.Steps[:3] {
		if !s.OK {
			t.Fatalf("step %q should have passed, got %q", s.Name, s.Error)
		}
	}
	// La purge a échoué : le dépôt de sonde est ENTIER dans la cible, le
	// signaler est d'autant plus nécessaire.
	if probe.Residue == "" {
		t.Fatal("residue must be reported when the probe repository could not be pruned")
	}
}

// Un backend qui accepte l'écriture mais ne restitue pas ce qu'on vient d'y
// écrire est en panne, pas en bonne santé. C'est précisément ce que `restic
// init` seul laisse passer.
func TestProbeFailsWhenTheWrittenSnapshotIsNotListedBack(t *testing.T) {
	rt := newProbeRuntime()
	rt.snapshotsJSON = `[]`
	e := New(rt, "restic/restic:latest")

	probe, err := e.TestTarget(context.Background(),
		localStorage(0, "offsite", "/srv/offsite", "pw"), nil)
	if err != nil {
		t.Fatalf("TestTarget() error: %v", err)
	}
	if probe.FailedStep() != domain.ProbeRead {
		t.Fatalf("failed step = %q, want %q", probe.FailedStep(), domain.ProbeRead)
	}
	if hasArg(rt.subcommands(), "forget") {
		t.Fatal("forget must not run when the read step failed: the snapshot ID is unknown")
	}
}

// Une paire copie/source en conflit d'identifiants est refusée AVANT tout
// accès réseau : la vérification est locale, la faire passer après l'init
// ferait payer un aller-retour pour un verdict déjà connu.
func TestProbeRefusesAConflictingCopyPairWithoutRunningAnything(t *testing.T) {
	rt := newProbeRuntime()
	e := New(rt, "restic/restic:latest")

	dst := &domain.StorageConfig{
		Name: "copy", Type: domain.StorageS3, Endpoint: "s3.example.com/copy",
		ResticPassword: "pw", CopyOf: 1,
		Credentials: map[string]string{"AWS_ACCESS_KEY_ID": "key-copy"},
	}
	src := &domain.StorageConfig{
		ID: 1, Name: "primary", Type: domain.StorageS3, Endpoint: "s3.example.com/primary",
		ResticPassword: "pw-src",
		Credentials:    map[string]string{"AWS_ACCESS_KEY_ID": "key-source"},
	}

	probe, err := e.TestTarget(context.Background(), dst, src)
	if err != nil {
		t.Fatalf("TestTarget() error: %v", err)
	}
	if probe.FailedStep() != domain.ProbePair {
		t.Fatalf("failed step = %q, want %q", probe.FailedStep(), domain.ProbePair)
	}
	if len(rt.specs) != 0 {
		t.Fatalf("%d workers started for a locally-detectable conflict, want 0", len(rt.specs))
	}
	// L'erreur doit NOMMER la variable fautive, sinon l'exploitant cherche.
	if !strings.Contains(probe.Steps[0].Error, "AWS_ACCESS_KEY_ID") {
		t.Fatalf("error must name the conflicting variable, got: %s", probe.Steps[0].Error)
	}
}

// Deux familles de syntaxe chez restic. Concaténer sans distinguer produirait
// `bucket/sonde` là où restic attend `bucket:sonde`, c'est-à-dire un nom de
// bucket inexistant — et la sonde échouerait pour une raison inventée par
// nous, en accusant les identifiants de l'exploitant.
func TestProbeEndpointJoinsAccordingToTheBackendFamily(t *testing.T) {
	const name = "sdb-probe-x"
	cases := []struct {
		storage  domain.StorageType
		endpoint string
		want     string
	}{
		{domain.StorageLocal, "/srv/offsite", "/srv/offsite/" + name},
		{domain.StorageLocal, "/srv/offsite/", "/srv/offsite/" + name},
		{domain.StorageS3, "s3.example.com/bucket/sdb", "s3.example.com/bucket/sdb/" + name},
		{domain.StorageSFTP, "user@host:/backups/sdb", "user@host:/backups/sdb/" + name},
		{domain.StorageREST, "https://host/sdb", "https://host/sdb/" + name},
		// racine de bucket : le séparateur est `:`, pas `/`
		{domain.StorageB2, "my-bucket", "my-bucket:" + name},
		{domain.StorageB2, "my-bucket:", "my-bucket:" + name},
		{domain.StorageB2, "my-bucket:/sdb", "my-bucket:/sdb/" + name},
		{domain.StorageAzure, "my-container", "my-container:" + name},
		{domain.StorageGCS, "my-bucket:/sdb", "my-bucket:/sdb/" + name},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/%s", tc.storage, tc.endpoint), func(t *testing.T) {
			if got := probeEndpoint(tc.storage, tc.endpoint, name); got != tc.want {
				t.Fatalf("probeEndpoint(%q, %q) = %q, want %q", tc.storage, tc.endpoint, got, tc.want)
			}
		})
	}
}

// Le dépôt de sonde est ordinaire et destructible : hériter du découpage d'une
// source n'a aucun sens pour un dépôt qu'on purge, et le cliquet append-only
// empêcherait justement l'étape qui teste la suppression.
func TestProbeConfigIsAnOrdinaryDestructibleRepository(t *testing.T) {
	src := localStorage(0, "offsite", "/srv/offsite", "pw")
	src.AppendOnly = true
	src.CopyOf = 7

	cfg, err := probeConfig(src)
	if err != nil {
		t.Fatalf("probeConfig() error: %v", err)
	}
	if cfg.AppendOnly {
		t.Fatal("probe repository must not be append-only: it has to delete what it wrote")
	}
	if cfg.CopyOf != 0 {
		t.Fatalf("probe repository CopyOf = %d, want 0", cfg.CopyOf)
	}
	if src.Endpoint != "/srv/offsite" {
		t.Fatalf("probeConfig mutated the caller's config: endpoint = %q", src.Endpoint)
	}
}
