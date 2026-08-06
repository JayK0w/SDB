package restic

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/standalone-docker-backup/sdb/internal/domain"
	"github.com/standalone-docker-backup/sdb/internal/infra/streamio"
)

// probeDirPrefix : préfixe du dépôt jetable de la sonde. Explicite et
// reconnaissable à l'œil dans un bucket — un résidu qui ressemblerait à des
// données serait pire que pas de sonde du tout.
const probeDirPrefix = "sdb-connectivity-probe-"

const (
	probeTag = "sdb-probe"
	// probeSource : Docker écrit /etc/hostname dans TOUT conteneur. Sonder
	// l'écriture avec ce fichier évite de monter quoi que ce soit de l'hôte
	// pour tester une cible qui n'a encore rien à voir avec une sauvegarde.
	probeSource = "/etc/hostname"
)

// TestTarget : éprouve la cible dans un sous-chemin dédié.
//
// Jamais à l'emplacement demandé : tester une configuration qui pointe sur un
// dépôt déjà rempli ne doit pas pouvoir l'abîmer, et un `init` sur un dépôt
// existant échouerait de toute façon pour la mauvaise raison.
//
// Convention de retour : un code de sortie restic non nul décrit la CIBLE
// (droit manquant, identifiants faux) et devient une étape en échec ; une
// erreur Go décrit notre PLOMBERIE (démon Docker injoignable) et remonte
// telle quelle. Confondre les deux ferait annoncer « vos identifiants sont
// mauvais » à un opérateur dont le démon est simplement arrêté.
func (e *Engine) TestTarget(ctx context.Context, storage, copySource *domain.StorageConfig) (*domain.TargetProbe, error) {
	probe := &domain.TargetProbe{}

	// Paire copie/source d'abord : la vérification est purement locale, elle
	// ne coûte aucun aller-retour réseau et disqualifie la configuration
	// entière si elle échoue.
	if copySource != nil {
		if _, err := copyContext(storage, copySource); err != nil {
			probe.Fail(domain.ProbePair, err)
			return probe, nil
		}
		probe.Pass(domain.ProbePair)
	}

	cfg, err := probeConfig(storage)
	if err != nil {
		return nil, err
	}
	// Contexte de dépôt constructible ? Type inconnu ou chemin local relatif
	// sont des défauts de CONFIGURATION : les laisser remonter en erreur Go
	// les ferait passer pour une panne d'infrastructure.
	if _, err := repositoryFor(cfg); err != nil {
		probe.Fail(domain.ProbeInit, err)
		return probe, nil
	}

	// 1. init — lister le backend et y écrire
	stderr := streamio.NewBounded(16 << 10)
	exit, err := e.run(ctx, cfg, []string{"init"}, nil, nil, io.Discard, stderr)
	if err != nil {
		return nil, err
	}
	if exit != 0 {
		probe.Fail(domain.ProbeInit, fmt.Errorf("restic init failed (exit %d): %s", exit, stderr))
		return probe, nil
	}
	probe.Pass(domain.ProbeInit)
	// À partir d'ici un dépôt existe dans la cible : le signaler même si la
	// suite échoue, sinon le résidu resterait sans que personne le sache.
	probe.Residue = cfg.Endpoint

	// 2. backup — écrire des données et poser un verrou
	stderr = streamio.NewBounded(16 << 10)
	exit, err = e.run(ctx, cfg, []string{"backup", probeSource, "--tag", probeTag}, nil, nil, io.Discard, stderr)
	if err != nil {
		return nil, err
	}
	if exit != 0 {
		probe.Fail(domain.ProbeWrite, fmt.Errorf("restic backup failed (exit %d): %s", exit, stderr))
		return probe, nil
	}
	probe.Pass(domain.ProbeWrite)

	// 3. snapshots — relire ce qu'on vient d'écrire
	snapshotID, err := e.probeSnapshotID(ctx, cfg, probe)
	if err != nil {
		return nil, err
	}
	if snapshotID == "" {
		return probe, nil
	}
	probe.Pass(domain.ProbeRead)

	// 4. forget --prune — SUPPRIMER, le droit que la création ne teste jamais
	stderr = streamio.NewBounded(16 << 10)
	exit, err = e.run(ctx, cfg, []string{"forget", snapshotID, "--prune"}, nil, nil, io.Discard, stderr)
	if err != nil {
		return nil, err
	}
	if exit != 0 {
		probe.Fail(domain.ProbeDelete, fmt.Errorf("restic forget failed (exit %d): %s", exit, stderr))
		return probe, nil
	}
	probe.Pass(domain.ProbeDelete)

	return probe, nil
}

// probeSnapshotID : identifiant du snapshot de sonde, "" si l'étape de lecture
// a échoué (l'échec est alors déjà consigné dans probe).
//
// Un dépôt qui accepte l'écriture mais ne restitue PAS le snapshot est un
// échec de lecture, pas un succès silencieux : c'est exactement la forme que
// prend un backend mal cloisonné, et c'est ce que `restic init` seul laisse
// passer.
func (e *Engine) probeSnapshotID(ctx context.Context, cfg *domain.StorageConfig,
	probe *domain.TargetProbe) (string, error) {

	var stdout bytes.Buffer
	stderr := streamio.NewBounded(16 << 10)
	exit, err := e.run(ctx, cfg, []string{"snapshots", "--json", "--tag", probeTag},
		nil, nil, &stdout, stderr)
	if err != nil {
		return "", err
	}
	if exit != 0 {
		probe.Fail(domain.ProbeRead, fmt.Errorf("restic snapshots failed (exit %d): %s", exit, stderr))
		return "", nil
	}

	var raw []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		probe.Fail(domain.ProbeRead, fmt.Errorf("parsing restic snapshots output: %w", err))
		return "", nil
	}
	if len(raw) == 0 || raw[0].ID == "" {
		probe.Fail(domain.ProbeRead, fmt.Errorf("the snapshot just written is not listed back by the repository"))
		return "", nil
	}
	return raw[0].ID, nil
}

// probeConfig : la configuration soumise, détournée vers un dépôt jetable.
func probeConfig(storage *domain.StorageConfig) (*domain.StorageConfig, error) {
	suffix, err := probeSuffix()
	if err != nil {
		return nil, err
	}
	out := *storage
	out.ID = 0
	// La sonde crée un dépôt ORDINAIRE : hériter des paramètres de découpage
	// d'une source n'a aucun sens pour un dépôt qu'on va détruire, et exigerait
	// d'ouvrir les deux backends pour ne rien prouver de plus.
	out.CopyOf = 0
	// Elle doit pouvoir supprimer ce qu'elle a écrit — le cliquet applicatif
	// protège les dépôts réels, pas celui-ci.
	out.AppendOnly = false
	out.Endpoint = probeEndpoint(storage.Type, storage.Endpoint, probeDirPrefix+suffix)
	return &out, nil
}

func probeSuffix() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating probe suffix: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// probeEndpoint : sous-chemin de sonde sous la cible.
//
// Deux familles de syntaxe chez restic : les backends à chemin (`hôte/bucket/
// chemin`, `/mnt/...`) où le séparateur est `/`, et ceux de forme
// `conteneur:chemin` où le premier séparateur est `:`. Concaténer sans
// distinguer produirait `bucket/sonde` là où restic attend `bucket:sonde`,
// c'est-à-dire un nom de bucket qui n'existe pas.
func probeEndpoint(t domain.StorageType, endpoint, name string) string {
	e := strings.TrimRight(endpoint, "/")
	switch t {
	case domain.StorageB2, domain.StorageAzure, domain.StorageGCS:
		if trimmed := strings.TrimRight(e, ":"); !strings.Contains(trimmed, ":") {
			return trimmed + ":" + name
		}
	}
	return e + "/" + name
}
