package httpapi

import (
	"errors"
	"testing"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// Le residu n est annonce que s il EXISTE. Une note de nettoyage sur une sonde
// qui n a rien pu creer enverrait l exploitant chercher un chemin absent, et
// lui ferait douter du verdict au moment ou il a besoin d y croire.
func TestProbeDTOAnnouncesResidueOnlyWhenSomethingWasCreated(t *testing.T) {
	failedBeforeInit := &domain.TargetProbe{}
	failedBeforeInit.Fail(domain.ProbeInit, errors.New("bad credentials"))

	got := toProbeDTO(failedBeforeInit)
	if got.Residue != "" || got.ResidueNote != "" {
		t.Fatalf("residue = %q / note = %q, want both empty", got.Residue, got.ResidueNote)
	}
	if got.OK {
		t.Fatal("ok = true although the only step failed")
	}
	if got.FailedStep != domain.ProbeInit {
		t.Fatalf("failed_step = %q, want %q", got.FailedStep, domain.ProbeInit)
	}

	created := &domain.TargetProbe{Residue: "/srv/offsite/sdb-connectivity-probe-abc"}
	created.Pass(domain.ProbeInit)
	created.Pass(domain.ProbeWrite)
	created.Pass(domain.ProbeRead)
	created.Pass(domain.ProbeDelete)

	got = toProbeDTO(created)
	if !got.OK {
		t.Fatalf("ok = false although every step passed: %+v", got.Steps)
	}
	if got.FailedStep != "" {
		t.Fatalf("failed_step = %q, want empty", got.FailedStep)
	}
	if got.Residue == "" || got.ResidueNote == "" {
		t.Fatalf("residue = %q / note = %q, want both set", got.Residue, got.ResidueNote)
	}
	if len(got.Steps) != 4 {
		t.Fatalf("%d steps, want 4", len(got.Steps))
	}
}

// Un compte rendu sans aucune etape n est pas un succes : c est une sonde qui
// n a rien fait. Le repondre OK ferait valider une cible jamais touchee.
func TestProbeDTOWithNoStepIsNotOK(t *testing.T) {
	if got := toProbeDTO(&domain.TargetProbe{}); got.OK {
		t.Fatal("an empty probe must not report ok")
	}
}
