# État des lieux — 6 août 2026

Point de reprise : ce que SDB fait aujourd'hui, ce que **ce déploiement-ci**
fait réellement, et ce qui reste à décider. Écrit pour qu'une session repartant
de zéro sache où on en est sans relire l'historique.

Documents voisins : [README](../README.md) (le produit),
[RUNBOOK](RUNBOOK.md) (l'exploitation).

---

## 1. Où en est le code

`main` = `2580eda`, poussé sur `JayK0w/SDB`, CI verte (backend `-race`,
intégration contre un vrai restic, frontend `vue-tsc` strict, image Docker).

**Travail en cours non poussé** : branche `storage-target-probe`, trois commits
au-dessus de `main` — la sonde de cible, son bouton, et la journalisation du
contrôle d'intégrité à la demande. Suite verte en local, `gofmt` propre, mais
**la CI ne les a jamais vus**.

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
| **Jauges de fraîcheur réamorcées au démarrage** | `2580eda` | Les jauges Prometheus vivent en mémoire : après un redémarrage, RPO et RTO disparaissaient et les alertes bâties dessus devenaient fausses **dans les deux sens**. Constaté en production le jour de l'activation de `/metrics`. |
| **Sonde de cible** — `POST /storage/test` | branche `storage-target-probe` | La création lance `restic init`, qui n'exerce que *lister* et *écrire* : une clé sans droit de **suppression** passe la création et ne casse qu'au premier retrait de verrou, sur la copie secondaire. La sonde exerce les quatre droits et nomme celui qui manque. |
| Bouton « Tester la cible » | branche `storage-target-probe` | L'endpoint n'était appelé par rien. Le verdict est rendu par droit, et **invalidé dès que le formulaire change**. |
| Contrôle d'intégrité à la demande journalisé | branche `storage-target-probe` | `POST /storage/:id/check` ne journalisait ni début ni fin : deux contrôles lancés en production n'ont laissé **aucune trace**, et « pas de nouvelles » ne permettait pas de distinguer « réussi » de « tourne encore ». La passe périodique, elle, journalisait déjà correctement. |

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
| **Copie secondaire `backup B2` (id 3)** | Backblaze B2, bucket `sdb-copie-Paulo` (région `eu-central-003`), racine du bucket, type `b2` |
| Planification `demo-web-quotidien` (id 2) | `0 2 * * *` UTC, conteneur `demo-web`, rétention `keep_daily=7 / keep_weekly=4 / keep_monthly=6` + prune |
| Sauvegarde de contrôle | success, snapshot `cfcac2a0`, 5 478 octets, fichiers vérifiés sur le disque Windows |
| Restaurabilité `backup-local` | **prouvée** — restauration #12 `system:verification`, succès en **1,4 s** |
| Restaurabilité `backup B2` | **prouvée** — restauration #14, snapshot `55699246`, succès en **3,97 s** |
| `SDB_METRICS_TOKEN` | renseigné, `/metrics` **actif** |
| Copies de la base avant chaque déploiement | `C:\Users\paull\Desktop\Backup\sdb-db-backup-*` |

Le chemin `/run/desktop/mnt/host/c/...` est la façon dont la VM Docker voit le
disque `C:`. C'est ce qui rend le dépôt durable, contrairement à `/tmp`.

### La règle 3-2-1 est satisfaite, et vérifiée

La copie secondaire a été créée le 4 août. Le rattrapage de l'existant
(`02a4c31`) a recopié les deux snapshots déjà présents **en 15 s**, `pending=0`.

Trois exemplaires (volume de production, `backup-local`, `backup B2`), deux
supports (disque `C:`, Backblaze), un hors site. Chaque maillon a été prouvé
par une **extraction réelle**, pas par un compteur vert — c'est la leçon de
l'incident du 4 août ci-dessus.

```
sdb_replication_pending_snapshots{copy="backup B2",source="backup-local"} 0
sdb_replication_lag_seconds{copy="backup B2",source="backup-local"} 0
sdb_verification_restore_duration_seconds{storage="backup B2"} 3.97
```

L'écart de 1,4 s à 3,97 s entre le dépôt local et B2, c'est le téléchargement
réel depuis Backblaze.

### Ce qui n'est PAS configuré ici

1. **`SDB_ALERT_WEBHOOK` vide** — un échec de la sauvegarde de 02:00 n'est
   visible que dans l'interface ou les logs. Signalé à chaque démarrage.
2. **Prometheus n'est pas déployé.** `/metrics` est lisible depuis le 4 août,
   mais **rien ne l'interroge** : les 7 ko de règles de
   `deploy/prometheus/sdb-alerts.yml` ne sont chargés par personne. Les
   mesures existent, les alertes non.

Ces deux points se règlent dans un même redémarrage et sont les prochaines
décisions attendues.

### Dette connue sur ce déploiement

- **L'Application Key B2 doit être révoquée** : elle a circulé en clair pendant
  la session de validation du 4 août. Créer une clé neuve restreinte au même
  bucket et la remplacer dans SDB.
- **Le bucket est en « Keep all versions »** — vérifié : les suppressions y
  produisent des *delete markers*. Sans la règle de cycle de vie *Keep only the
  last version*, chaque fichier supprimé par restic reste facturé
  indéfiniment.
- **Le dépôt B2 est en type `b2`** alors que la documentation de restic
  recommande son API S3-compatible (défauts de gestion d'erreurs dans la
  bibliothèque B2). Migrable **sans rien re-téléverser** : le dépôt est à la
  racine du bucket, `s3:s3.eu-central-003.backblazeb2.com/sdb-copie-Paulo`
  désigne les mêmes octets. Une édition suffit — à grouper avec la rotation de
  clé ci-dessus.

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
- **Contrôles d'intégrité** : `grep 'integrity check'` — début et fin sont
  journalisés, avec `trigger=periodic` ou `trigger=on-demand` et la durée. Une
  ligne `started` sans `passed` ni `failed` signale un contrôle encore en cours,
  ou interrompu.
- **Une cible avant de lui confier quoi que ce soit** : `POST /storage/test`
  exerce lister, écrire, relire et **supprimer**. La création n'exerce que les
  deux premiers.

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
- **La sonde de cible laisse un résidu** : restic ne sait pas détruire un
  dépôt. `forget --prune` retire les paquets, index et snapshots ; `config` et
  `keys/` restent — **604 octets, mesurés**. La réponse rend le chemin exact
  plutôt que de se taire. L'alternative — élargir `WorkerSpec` d'un
  *entrypoint* pour lancer un shell — a été écartée : ouvrir le runtime à
  autre chose que restic pour 604 octets est un mauvais échange.
- **Un volume Docker subsiste après un test d'intégration** :
  `TestIntegrationRestoreUnknownSnapshotFails` crée son volume cible avant que
  restic échoue et ne le nettoie pas.

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
- Sous PowerShell, `docker run --entrypoint sh -c '…'` **tronque la sortie** dès
  qu'une chaîne passée à `echo` contient une espace. Enchaîner des `docker run`
  distincts plutôt que de scripter dans un shell intermédiaire.
- Une **pile jetable** valide un changement sans toucher à la production :
  image construite sous un tag distinct, `docker run` sur le port 8081, volume
  et secrets propres, `SDB_ADMIN_PASSWORD` fixé pour ouvrir une session. Le
  conteneur `sdb` n'est ni arrêté ni redéployé.
