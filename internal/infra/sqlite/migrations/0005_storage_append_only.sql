-- v0.5 : depots append-only. SDB detient le socket Docker ET le mot de passe
-- du depot : sans garde-fou, sa compromission -- ou une simple erreur de
-- politique de retention -- detruit les sauvegardes en meme temps que la
-- production. Un depot marque append_only refuse forget, prune et sa propre
-- suppression cote SDB.
--
-- Ce verrou est APPLICATIF. Il ne remplace pas l'immuabilite cote serveur
-- (rest-server --append-only, S3 Object Lock, versioning + MFA delete) : il
-- retire seulement SDB de la liste des vecteurs de destruction.
--
-- Defaut 0 : les depots existants gardent leur comportement actuel, le
-- passage en append-only reste un choix explicite.

ALTER TABLE storage_configs ADD COLUMN append_only INTEGER NOT NULL DEFAULT 0;
