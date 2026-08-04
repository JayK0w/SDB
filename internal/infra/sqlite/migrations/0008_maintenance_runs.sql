-- Date du dernier passage des boucles periodiques.
--
-- Jusqu'ici chaque boucle (verification de restaurabilite, controle
-- d'integrite, reconciliation des copies) attendait un intervalle COMPLET
-- apres le demarrage avant son premier passage. L'intention etait d'epargner
-- l'hote au boot ; l'effet est qu'un redemarrage remet le compteur a zero.
--
-- Sur une instance redemarree plus souvent que l'intervalle -- une mise a jour
-- par semaine avec SDB_VERIFY_INTERVAL=168h suffit -- la passe ne s'executait
-- JAMAIS. En silence : rien ne distingue une garantie qui ne s'arme pas d'une
-- garantie qui va bien.
--
-- Une ligne par tache, ecrite apres chaque passage (reussi ou non : le passage
-- a bien eu lieu, son resultat part par les alertes et les metriques).

CREATE TABLE IF NOT EXISTS maintenance_runs (
    task        TEXT PRIMARY KEY,
    last_run_at TEXT NOT NULL
);
