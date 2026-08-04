# État des lieux — 4 août 2026

Point de reprise : ce que SDB fait aujourd'hui, ce que **ce déploiement-ci**
fait réellement, et ce qui reste à décider. Écrit pour qu'une session repartant
de zéro sache où on en est sans relire l'historique.

Documents voisins : [README](../README.md) (le produit),
[RUNBOOK](RUNBOOK.md) (l'exploitation).

---

## 1. Où en est le code

`main` = `7d0ffd1`, poussé sur `JayK0w/SDB`, CI verte (backend `-race`,
intégration contre un vrai restic, frontend `vue-tsc` strict, image Docker).

### Les six phases initiales, puis v0.2 — terminées

Domaine + ports, infra (SQLite, crypto, restic, Docker), usecases, API + WS,
frontend Vue 3, packaging durci. Puis v0.2 : planifications cron, historique
des restaurations, backends cloud, `/metrics`, frontend TypeScript.

### Les cinq chantiers de l'audit du 31 juillet — terminés

| # | Chantier | Commit |
|---|---|---|
| 1 | `govulncheck` en CI + correction de 5 CVE atteignables | `94cfc91` |
| 2 | Sessions JWT révocables (génération de jetons en base) | `f94efac` |
| 3 | Tests d'intégration contre un vrai restic | `fe57035` |
| 4 | **Copie secondaire (3-2-1)** — `restic copy` vers un second dépôt | `102b472` |
| 5 | **Runbook, RTO/RPO mesurés, rotation de la clé maître** | `5369901` |

### Ce qui a suivi, en réponse à l'usage réel

| Sujet | Commit | Pourquoi |
|---|---|---|
| Copie secondaire activable **après coup** | `02a4c31` | Brancher une copie sur un dépôt déjà rempli ne recopiait pas l'historique avant la passe suivante (jusqu'à 6 h). La création déclenche désormais la recopie de l'existant. |
| **Échéances périodiques persistées** | `93db16e` | Les trois boucles repartaient d'un intervalle complet à chaque démarrage : une instance redémarrée plus souvent que l'intervalle ne vérifiait, ne contrôlait et ne répliquait **jamais** — en silence. |
| `POST /storage/:id/verify` | `7d0ffd1` | Aucun moyen de dire « prouve-moi maintenant que ce dépôt est restaurable ». Un dépôt neuf attendait une semaine. |

---

## 2. Où en est CE déploiement

Conteneur `sdb`, volume `projet_sdb-data`, API sur `127.0.0.1:8080`.

### L'incident du 4 août — dépôt de production perdu

Le dépôt `test-local` pointait sur `/tmp/sdb-test-repo`, c'est-à-dire le `/tmp`
**de la VM Docker Desktop**, purgé au redémarrage de la VM. Les 35 sauvegardes
« réussies » de l'historique ne correspondaient à **aucune donnée
récupérable** : le répertoire était vide.

Découvert cinq minutes après que le correctif d'échéances a armé les passes
pour la première fois — `restic check` et la vérification ont échoué toutes
les deux en `exit 10 : repository does not exist`. Avant ce correctif, les
passes ne s'exécutaient jamais : l'instance affichait 35 runs verts et zéro
erreur depuis un mois.

Le dépôt a été renommé **`test-local-perdu`** (impossible à supprimer :
l'historique le référence encore).

### Ce qui tourne maintenant

| Élément | Valeur |
|---|---|
| Dépôt `backup-local` (id 2) | `/run/desktop/mnt/host/c/Users/paull/Desktop/Backup/sdb-repo`, soit `C:\Users\paull\Desktop\Backup\sdb-repo` |
| Planification `demo-web-quotidien` (id 2) | `0 2 * * *` UTC, conteneur `demo-web`, rétention `keep_daily=7 / keep_weekly=4 / keep_monthly=6` + prune |
| Sauvegarde de contrôle | success, snapshot `cfcac2a0`, 5 478 octets, fichiers vérifiés sur le disque Windows |
| Restaurabilité | **prouvée** — restauration #12 `system:verification`, succès en **1,4 s**, volume jetable détruit |
| Copies de la base avant chaque déploiement | `C:\Users\paull\Desktop\Backup\sdb-db-backup-*` |

Le chemin `/run/desktop/mnt/host/c/...` est la façon dont la VM Docker voit le
disque `C:`. C'est ce qui rend le dépôt durable, contrairement à `/tmp`.

### Ce qui n'est PAS configuré ici

1. **Aucune copie secondaire** — le dépôt vit sur un support unique. Le
   logiciel le signale à chaque démarrage et sur la page Stockage.
2. **`SDB_ALERT_WEBHOOK` vide** — un échec de la sauvegarde de 02:00 n'est
   visible que dans l'interface ou les logs.
3. **`SDB_METRICS_TOKEN` vide**, donc `/metrics` **désactivé** : les mesures
   de RPO, de RTO, de retard de réplication et de fenêtres manquées existent
   en mémoire mais personne ne peut les lire, et
   `deploy/prometheus/sdb-alerts.yml` n'a rien à interroger.

Ces trois points sont les prochaines décisions attendues. Les deux derniers se
règlent dans un même redémarrage.

---

## 3. Ce que le produit garantit, et comment le vérifier

- **RPO** : `sdb_last_backup_success_timestamp_seconds` donne l'âge réel de la
  dernière sauvegarde réussie, par conteneur.
- **RTO** : `sdb_verification_restore_duration_seconds` est la durée d'une
  restauration **réelle**, chronométrée à chaque vérification.
- **Seconde copie** : `sdb_replication_pending_snapshots` et
  `sdb_replication_lag_seconds`, mesurés en comparant les deux dépôts, jamais
  mémorisés.
- **Échéances** : `docker compose logs sdb | grep 'periodic task armed'` — un
  `first_pass_in` égal à l'intervalle complet après chaque redémarrage
  signalerait une régression du correctif `93db16e`.

Procédures détaillées : [RUNBOOK](RUNBOOK.md).

---

## 4. Limites assumées, documentées, non corrigées

- **Pas de haute disponibilité** : SDB arrêté = aucune sauvegarde. Les fenêtres
  manquées sont détectées, pas évitées.
- **Le mot de passe d'un dépôt restic ne tourne pas** (restic en dérive ses
  clés, l'API le refuse). Seule la clé maître tourne, via
  `sdb rotate-master-key`.
- **Socket Docker monté en direct**, sans proxy : SDB est root-équivalent sur
  son hôte.
- **Configuration non déclarative** : dépôts et planifications n'existent que
  dans `sdb.db`, ils ne se reconstruisent pas depuis git.
- **`append_only` est un verrou applicatif**, pas de l'immuabilité : il retire
  SDB des vecteurs de destruction, il ne protège pas d'un accès direct au
  dépôt.
- **Copier entre deux comptes S3 distincts est impossible** : restic partage
  les identifiants de backend entre un dépôt et sa source de copie. La paire
  est refusée à la configuration, en nommant la variable en conflit.

---

## 5. Environnement de travail

- Go 1.26.5 et Node 24.18 installés localement : `go build/vet/test`,
  `make test-integration` et `npm run type-check` tournent sur la machine.
- `go test -race` **ne tourne pas** en local (exige cgo/gcc) — c'est la CI qui
  le couvre.
- `gofmt -l` signale tous les fichiers en CRLF : vérifier le formatage dans un
  conteneur Linux avec `dos2unix`. `internal/api/http/dto.go` et
  `internal/infra/restic/restore_test.go` sont non conformes de longue date.
- Ne jamais réécrire un fichier source avec `Set-Content` : l'encodage des
  accents est corrompu au passage.
- Docker Desktop doit être démarré avant tout test d'intégration.
