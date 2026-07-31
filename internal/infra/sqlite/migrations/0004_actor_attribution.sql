-- v0.4 : attribution. Sauvegardes et restaurations sont tracees a leur
-- auteur : sans ca l'historique ne peut pas repondre a "qui a lance cette
-- restauration destructrice ?", ce qu'exige tout contexte de conformite.
--
-- triggered_by_id : id utilisateur, 0 = declenchement interne (planificateur).
-- triggered_by    : libelle stable ("alice", "system:schedule:nightly").
-- Pas de cle etrangere vers users : l'historique doit survivre a la
-- suppression d'un compte, sinon l'audit disparait avec l'utilisateur.
--
-- Les lignes anterieures a cette migration n'ont pas d'auteur connu : le
-- defaut vide les marque comme telles plutot que de leur en inventer un.

ALTER TABLE backups_history  ADD COLUMN triggered_by_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE backups_history  ADD COLUMN triggered_by    TEXT    NOT NULL DEFAULT '';
ALTER TABLE restores_history ADD COLUMN triggered_by_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE restores_history ADD COLUMN triggered_by    TEXT    NOT NULL DEFAULT '';

CREATE INDEX idx_history_actor  ON backups_history  (triggered_by);
CREATE INDEX idx_restores_actor ON restores_history (triggered_by);
