-- v0.3 : clonage de volume. source_volume garde le volume tel qu'archive
-- dans le snapshot ; vide = restauration en place (source = cible), valeur
-- differente de target_volume = clone vers un volume neuf.
-- Les lignes anterieures sont toutes des restaurations en place : le defaut
-- vide les decrit correctement.

ALTER TABLE restores_history ADD COLUMN source_volume TEXT NOT NULL DEFAULT '';
