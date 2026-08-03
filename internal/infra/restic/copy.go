package restic

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
	"github.com/standalone-docker-backup/sdb/internal/infra/streamio"
)

// EnsureCopyTarget : le dépôt de copie existe et partage les paramètres de
// découpage de sa source.
//
// L'héritage n'est possible qu'À L'INITIALISATION : un dépôt déjà créé garde
// les siens définitivement. C'est sans conséquence sur la validité des copies
// — restic recopie les blocs tels quels — mais les données peuvent alors
// occuper jusqu'au double dans la copie si elle reçoit un jour des blocs
// découpés autrement.
func (e *Engine) EnsureCopyTarget(ctx context.Context, dst, src *domain.StorageConfig) error {
	// sonde authentifiée la moins chère, sur la CIBLE seule
	exit, err := e.run(ctx, dst, []string{"cat", "config"}, nil, nil, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if exit == 0 {
		return nil
	}

	repo, err := copyContext(dst, src)
	if err != nil {
		return err
	}
	stderr := streamio.NewBounded(16 << 10)
	exit, err = e.runIn(ctx, repo, []string{"init", "--copy-chunker-params"}, nil, nil, io.Discard, stderr)
	if err != nil {
		return err
	}
	if exit != 0 {
		return fmt.Errorf("initialising copy target %q from %q failed (exit %d): %s",
			dst.Name, src.Name, exit, stderr)
	}
	return nil
}

// Copy : recopie des snapshots de src vers dst.
//
// restic saute ceux qui s'y trouvent déjà (il les reconnaît à leurs
// métadonnées, pas à leur identifiant — la ré-encryption en donne un
// nouveau) : relancer la commande est donc sûr et bon marché, ce qui rend la
// réconciliation périodique possible sans état côté SDB.
func (e *Engine) Copy(ctx context.Context, dst, src *domain.StorageConfig,
	snapshotIDs []string, events chan<- domain.ProgressEvent) error {

	repo, err := copyContext(dst, src)
	if err != nil {
		return err
	}

	// `copy` n'a pas de sortie --json : les lignes brutes partent telles
	// quelles dans le flux d'événements.
	emit := func(typ domain.EventType) func(string) {
		return func(line string) {
			send(ctx, events, domain.ProgressEvent{
				Type:    typ,
				Time:    time.Now().UTC(),
				Message: line,
			})
		}
	}
	stdout := &streamio.LineWriter{Emit: emit(domain.EventLog)}
	stderrBuf := streamio.NewBounded(32 << 10)
	stderr := &streamio.LineWriter{Emit: func(line string) {
		stderrBuf.Write([]byte(line + "\n"))
		emit(domain.EventError)(line)
	}}

	cmd := append([]string{"copy"}, snapshotIDs...)
	exit, err := e.runIn(ctx, repo, cmd, nil, nil, stdout, stderr)
	stdout.Flush()
	stderr.Flush()
	if err != nil {
		return err
	}
	if exit != 0 {
		return fmt.Errorf("restic copy from %q to %q failed (exit %d): %s",
			src.Name, dst.Name, exit, stderrBuf)
	}
	return nil
}
