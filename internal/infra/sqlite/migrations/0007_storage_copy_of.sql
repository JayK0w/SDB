-- Copie secondaire (regle 3-2-1). Jusqu'ici un depot unique par sauvegarde :
-- si ce support est perdu ou corrompu, TOUT est perdu. Le verrou append-only
-- protege de la suppression, pas de la perte du support.
--
-- Le lien est porte par la COPIE (copy_of_storage_id) et non par la source :
-- c'est la seule facon d'initialiser le depot secondaire avec les parametres
-- de decoupage de sa source (restic init --copy-chunker-params), et ca permet
-- plusieurs copies d'un meme depot sans colonne supplementaire.
--
-- REFERENCES sans ON DELETE : SQLite refuse alors de supprimer un depot encore
-- reference comme source. Perdre la source en laissant une copie orpheline
-- ferait croire a une replication qui n'a plus lieu.
--
-- NULL = depot principal, alimente par des sauvegardes.

ALTER TABLE storage_configs
    ADD COLUMN copy_of_storage_id INTEGER REFERENCES storage_configs(id);

CREATE INDEX IF NOT EXISTS idx_storage_configs_copy_of
    ON storage_configs(copy_of_storage_id);
