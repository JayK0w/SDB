package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

func (s *Server) handleListStorage(c *gin.Context) {
	configs, err := s.svc.Storages.List(c.Request.Context())
	if err != nil {
		s.respondError(c, err)
		return
	}
	out := make([]storageDTO, 0, len(configs))
	for _, cfg := range configs {
		out = append(out, toStorageDTO(cfg))
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) handleGetStorage(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	cfg, err := s.svc.Storages.Get(c.Request.Context(), id)
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toStorageDTO(*cfg))
}

func (s *Server) handleCreateStorage(c *gin.Context) {
	var req storageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondError(c, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err))
		return
	}
	cfg := req.toDomain(0)
	if err := s.svc.Storages.Create(c.Request.Context(), cfg); err != nil {
		s.respondError(c, err)
		return
	}
	// unique restitution du mot de passe : l administrateur doit pouvoir le
	// sequestrer hors de SDB
	c.JSON(http.StatusCreated, toStorageCreatedDTO(*cfg))
}

// probeTimeout : plafond d'un test de cible. Généreux pour un lien lent, mais
// borné : un opérateur attend devant son formulaire.
const probeTimeout = 3 * time.Minute

// handleTestStorage : éprouve une cible SANS la créer.
//
// Synchrone, contrairement à /check et /verify : ces deux-là peuvent durer des
// heures sur un gros dépôt, la sonde travaille sur un dépôt qu'elle vient de
// créer et qui contient un seul fichier. Répondre 202 obligerait l'interface à
// aller pêcher le résultat ailleurs pour une réponse qui arrive en quelques
// secondes.
//
// 200 même quand une étape échoue : la sonde a fait son travail, son verdict
// EST la réponse. Un 4xx ferait passer « ta clé n'a pas le droit de
// supprimer » pour une requête malformée.
func (s *Server) handleTestStorage(c *gin.Context) {
	var req storageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondError(c, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err))
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), probeTimeout)
	defer cancel()

	probe, err := s.svc.Storages.TestTarget(ctx, req.toDomain(0))
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toProbeDTO(probe))
}

func (s *Server) handleUpdateStorage(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	var req storageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondError(c, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err))
		return
	}
	cfg := req.toDomain(id)
	if err := s.svc.Storages.Update(c.Request.Context(), cfg); err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toStorageDTO(*cfg))
}

func (s *Server) handleDeleteStorage(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	if err := s.svc.Storages.Delete(c.Request.Context(), id); err != nil {
		s.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleListSnapshots(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	snapshots, err := s.svc.Storages.Snapshots(c.Request.Context(), id, c.QueryArray("tag"))
	if err != nil {
		s.respondError(c, err)
		return
	}
	out := make([]snapshotDTO, 0, len(snapshots))
	for _, snap := range snapshots {
		out = append(out, toSnapshotDTO(snap))
	}
	c.JSON(http.StatusOK, out)
}

// handleReplicationStatus : écart entre chaque dépôt et sa copie secondaire.
// La mesure INTERROGE les deux dépôts (deux `restic snapshots` par paire) :
// c'est une action à la demande, pas une donnée à rafraîchir en boucle.
func (s *Server) handleReplicationStatus(c *gin.Context) {
	statuses, err := s.svc.Replication.StatusAll(c.Request.Context())
	out := make([]replicationDTO, 0, len(statuses))
	for _, st := range statuses {
		out = append(out, toReplicationDTO(st))
	}
	if err != nil {
		// une paire injoignable ne doit pas effacer l'état des autres : on rend
		// ce qui a pu être mesuré, avec la raison de ce qui manque
		s.logger.Error("replication status incomplete", "error", err)
		c.JSON(http.StatusOK, gin.H{"replication": out, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"replication": out})
}

// handleReplicate : une copie complète peut durer des heures → job détaché,
// 202. L'avancement passe par le flux d'événements, le résultat par les logs
// et par GET /replication.
func (s *Server) handleReplicate(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	// la paire est validée AVANT d'accepter le job : un 202 sur un dépôt qui
	// n'est pas une copie secondaire serait un faux acquittement
	if _, err := s.svc.Replication.Status(c.Request.Context(), id); err != nil {
		s.respondError(c, err)
		return
	}
	go func() {
		copyCtx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
		defer cancel()
		st, err := s.svc.Replication.Replicate(copyCtx, id)
		if err != nil {
			s.logger.Error("on-demand replication failed", "storage_id", id, "error", err)
			s.hub.Publish(domain.ProgressEvent{
				Type:    domain.EventError,
				Time:    time.Now().UTC(),
				Message: fmt.Sprintf("replication of storage %d failed: %v", id, err),
			})
			return
		}
		s.hub.Publish(domain.ProgressEvent{
			Type: domain.EventLog,
			Time: time.Now().UTC(),
			Message: fmt.Sprintf("replication of %s finished: %d snapshot(s) copied, %d pending",
				st.CopyName, st.CopiedSnapshots, st.Pending),
		})
	}()
	c.JSON(http.StatusAccepted, gin.H{"status": "accepted"})
}

// handleVerifyStorage : restauration de VÉRIFICATION à la demande.
//
// Symétrique de /check, mais ce n'est pas la même preuve : `restic check`
// valide la structure du dépôt, la vérification extrait réellement le dernier
// snapshot dans un volume jetable et recompare les empreintes. Sans cette
// route, la seule façon d'obtenir cette preuve était d'attendre la passe
// planifiée — jusqu'à SDB_VERIFY_INTERVAL, soit une semaine par défaut après
// un dépôt fraîchement créé.
func (s *Server) handleVerifyStorage(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	// existence vérifiée avant d'accepter le job : un 202 sur un dépôt
	// inexistant serait un faux acquittement
	if _, err := s.svc.Storages.Get(c.Request.Context(), id); err != nil {
		s.respondError(c, err)
		return
	}
	go func() {
		verifyCtx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
		defer cancel()
		rec, err := s.svc.Verification.VerifyStorage(verifyCtx, id)
		switch {
		case err != nil:
			s.logger.Error("on-demand verification failed", "storage_id", id, "error", err)
			s.hub.Publish(domain.ProgressEvent{
				Type:    domain.EventError,
				Time:    time.Now().UTC(),
				Message: fmt.Sprintf("verification of storage %d failed: %v", id, err),
			})
		case rec == nil:
			// dépôt vide : rien à prouver, et surtout pas un échec
			s.hub.Publish(domain.ProgressEvent{
				Type:    domain.EventLog,
				Time:    time.Now().UTC(),
				Message: fmt.Sprintf("verification of storage %d skipped: repository has no snapshot", id),
			})
		default:
			s.hub.Publish(domain.ProgressEvent{
				Type:    domain.EventLog,
				Time:    time.Now().UTC(),
				Message: fmt.Sprintf("storage %d is provably restorable (restore #%d)", id, rec.ID),
			})
		}
	}()
	c.JSON(http.StatusAccepted, gin.H{"status": "accepted"})
}

// handleCheckStorage : restic check peut durer des minutes → exécution en
// arrière-plan, résultat via le flux d'événements et les logs (202).
func (s *Server) handleCheckStorage(c *gin.Context) {
	id, err := pathID(c)
	if err != nil {
		s.respondError(c, err)
		return
	}
	// existence vérifiée avant d'accepter le job
	if _, err := s.svc.Storages.Get(c.Request.Context(), id); err != nil {
		s.respondError(c, err)
		return
	}
	go func() {
		checkCtx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		// le service journalise début, succès et échec (avec le NOM du dépôt,
		// pas son identifiant) : re-journaliser ici doublerait chaque échec
		if err := s.svc.Storages.CheckIntegrity(checkCtx, id); err != nil {
			s.hub.Publish(domain.ProgressEvent{
				Type:    domain.EventError,
				Time:    time.Now().UTC(),
				Message: fmt.Sprintf("integrity check of storage %d failed: %v", id, err),
			})
			return
		}
		s.hub.Publish(domain.ProgressEvent{
			Type:    domain.EventLog,
			Time:    time.Now().UTC(),
			Message: fmt.Sprintf("integrity check of storage %d passed", id),
		})
	}()
	c.JSON(http.StatusAccepted, gin.H{"status": "accepted"})
}
