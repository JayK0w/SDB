package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

func probeRequest() *domain.StorageConfig {
	return &domain.StorageConfig{
		Name: "offsite", Type: domain.StorageS3, Endpoint: "s3.example.com/bucket/sdb",
		Credentials: map[string]string{"AWS_ACCESS_KEY_ID": "key"},
	}
}

// La promesse de la route : une cible refusee ne laisse RIEN a nettoyer. Si
// elle ecrivait en base, l'exploitant devrait supprimer une configuration
// morte apres chaque essai — et une configuration a moitie creee est
// exactement ce que la creation prend soin d'annuler.
func TestTestTargetPersistsNothing(t *testing.T) {
	svc, storages, _ := newStorageFixture()

	if _, err := svc.TestTarget(context.Background(), probeRequest()); err != nil {
		t.Fatalf("TestTarget() error: %v", err)
	}

	cfgs, err := storages.List(context.Background())
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(cfgs) != 0 {
		t.Fatalf("%d storage(s) written to the repository, want 0", len(cfgs))
	}
}

// Le mot de passe du depot de sonde est genere ici et n'est jamais restitue :
// le depot teste est detruit derriere, en faire porter un par la requete
// laisserait croire qu'il sera conserve.
func TestTestTargetGeneratesItsOwnThrowawayPassword(t *testing.T) {
	svc, _, engine := newStorageFixture()
	req := probeRequest()

	if _, err := svc.TestTarget(context.Background(), req); err != nil {
		t.Fatalf("TestTarget() error: %v", err)
	}

	calls := engine.probes()
	if len(calls) != 1 {
		t.Fatalf("%d probe calls, want 1", len(calls))
	}
	if calls[0].storage.ResticPassword == "" {
		t.Fatal("probe ran without a repository password")
	}
	// La configuration de l'appelant ne doit pas etre mutee : elle repart
	// vers la creation si l'exploitant valide le formulaire.
	if req.ResticPassword != "" {
		t.Fatalf("caller config was mutated: password = %q", req.ResticPassword)
	}
}

// Une copie secondaire se teste AVEC sa source : c'est la paire qui peut etre
// invalide (identifiants de backend partages par restic), pas la cible seule.
func TestTestTargetPassesTheDeclaredCopySource(t *testing.T) {
	svc, storages, engine := newStorageFixture()
	source := &domain.StorageConfig{
		Name: "primary", Type: domain.StorageLocal, Endpoint: "/srv/primary",
		ResticPassword: "pw-primary",
	}
	if err := storages.Create(context.Background(), source); err != nil {
		t.Fatalf("seeding source: %v", err)
	}

	req := probeRequest()
	req.CopyOf = source.ID
	if _, err := svc.TestTarget(context.Background(), req); err != nil {
		t.Fatalf("TestTarget() error: %v", err)
	}

	calls := engine.probes()
	if len(calls) != 1 || calls[0].copySource == nil {
		t.Fatalf("probe ran without the copy source: %+v", calls)
	}
	if calls[0].copySource.ID != source.ID {
		t.Fatalf("copy source ID = %d, want %d", calls[0].copySource.ID, source.ID)
	}
}

// Une chaine de copies est refusee a la creation ; la sonde doit refuser la
// meme configuration, sinon elle validerait ce que la creation rejettera.
func TestTestTargetRefusesACopyOfACopy(t *testing.T) {
	svc, storages, engine := newStorageFixture()
	copyCfg := &domain.StorageConfig{
		Name: "already-a-copy", Type: domain.StorageLocal, Endpoint: "/srv/copy",
		ResticPassword: "pw", CopyOf: 42,
	}
	if err := storages.Create(context.Background(), copyCfg); err != nil {
		t.Fatalf("seeding copy: %v", err)
	}

	req := probeRequest()
	req.CopyOf = copyCfg.ID
	_, err := svc.TestTarget(context.Background(), req)
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
	if len(engine.probes()) != 0 {
		t.Fatal("probe ran despite an invalid copy chain")
	}
}

// Une configuration invalide est refusee avant d'ouvrir quoi que ce soit :
// lancer un conteneur pour apprendre qu'il manque un point de terminaison
// serait du temps et des identifiants envoyes pour rien.
func TestTestTargetValidatesBeforeProbing(t *testing.T) {
	svc, _, engine := newStorageFixture()
	req := probeRequest()
	req.Endpoint = ""

	if _, err := svc.TestTarget(context.Background(), req); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
	if len(engine.probes()) != 0 {
		t.Fatal("probe ran on an invalid configuration")
	}
}
