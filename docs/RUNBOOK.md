# Runbook SDB

Document d'exploitation. Il répond à une seule question : **quoi faire,
maintenant, quand quelque chose ne va pas** — et ce que le système garantit
réellement, mesures à l'appui.

Il suppose le déploiement de référence : `docker-compose.yml` à la racine,
API sur `127.0.0.1:8080`, base dans le volume `sdb-data` (`/data/sdb.db`).

---

## 0. Table de décision

| Symptôme | Section |
|---|---|
| Alerte `BackupStale` / une sauvegarde est en échec | [Une sauvegarde échoue](#une-sauvegarde-échoue) |
| Un run finit en `warning` | [Un run finit en avertissement](#un-run-finit-en-avertissement) |
| Alerte `VerificationStale` / une vérification échoue | [Une vérification échoue](#une-vérification-échoue) |
| `restic check` en échec, dépôt suspect | [Le dépôt est corrompu](#le-dépôt-est-corrompu) |
| Alerte `ReplicationPending` / `ReplicationLagHigh` | [La copie secondaire a décroché](#la-copie-secondaire-a-décroché) |
| Alerte `ScheduleWindowMissed` | [Une fenêtre planifiée a été manquée](#une-fenêtre-planifiée-a-été-manquée) |
| SDB ne répond plus / ne démarre plus | [SDB ne démarre plus](#sdb-ne-démarre-plus) |
| Il faut restaurer des données | [Restaurer un volume](#restaurer-un-volume) |
| Le dépôt principal est perdu | [Sinistre : le dépôt principal est perdu](#sinistre--le-dépôt-principal-est-perdu) |
| `sdb.db` est perdu | [Sinistre : la base de SDB est perdue](#sinistre--la-base-de-sdb-est-perdue) |
| Une clé a fuité | [Rotation des clés](#rotation-des-clés) |

**Réflexe commun aux incidents :** les logs d'abord, ils nomment la cause.

```bash
docker compose logs --tail=200 sdb
```

---

## 1. RPO et RTO

### Ce que ces chiffres veulent dire ici

- **RPO** (perte de données acceptable) = intervalle entre deux sauvegardes
  **réussies**, plus le temps de détection d'un échec.
- **RTO** (temps de remise en service) = durée d'une restauration réelle,
  plus le temps de redémarrer le service qui consomme le volume.

### Ils ne sont pas déclarés, ils sont mesurés

SDB expose les deux grandeurs sur `/metrics` — elles ne reposent sur aucune
estimation :

| Grandeur | Métrique | Ce qui la produit |
|---|---|---|
| Âge de la dernière sauvegarde réussie, par conteneur | `sdb_last_backup_success_timestamp_seconds` | chaque run terminé |
| Durée d'une restauration **réelle** | `sdb_verification_restore_duration_seconds` | la restauration de vérification, chronométrée |
| Fraîcheur de la preuve de restaurabilité | `sdb_verification_last_success_timestamp_seconds` | idem |
| Retard de la seconde copie | `sdb_replication_lag_seconds` | comparaison des deux dépôts |

Relever les valeurs de **ce** déploiement :

```bash
TOKEN=$(grep SDB_METRICS_TOKEN .env | cut -d= -f2)
curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8080/metrics \
  | grep -E 'sdb_(last_backup_success|verification_restore_duration|verification_last_success|replication_lag)'
```

### Formuler l'engagement

Pour une planification quotidienne à 02:00 et une restauration de vérification
mesurée à *D* secondes :

- **RPO ≈ 24 h** (au pire, la dernière sauvegarde date de la veille) **+ le
  délai de détection** : sans webhook d'alerte configuré, un échec peut passer
  inaperçu jusqu'au prochain regard sur l'interface. Avec `SDB_ALERT_WEBHOOK`,
  la détection est immédiate.
- **RTO ≈ D + temps de redémarrage du service.** *D* concerne le **dernier**
  snapshot du dépôt ; une restauration d'un volume plus gros ou depuis un
  dépôt distant sera plus lente. Le chiffre à annoncer est celui mesuré sur le
  dépôt réellement utilisé, pas un ordre de grandeur.

Ces deux chiffres ne valent que si les mesures existent : **activer
`SDB_VERIFY_INTERVAL`** (désactivé par défaut) ou le RTO redevient une
promesse. Idem pour `SDB_ALERT_WEBHOOK` et le RPO.

### Ce qui casse ces engagements

| Ce qui arrive | Effet |
|---|---|
| SDB est à l'arrêt | Aucune sauvegarde. Pas de haute disponibilité, rien ne prend le relais. |
| Le conteneur source est arrêté au moment de la fenêtre | La sauvegarde tourne quand même (volumes montés en lecture seule). |
| L'hôte Docker est perdu | Les dépôts `local` partent avec lui : c'est le rôle de la copie secondaire hors-site. |
| SDB est redémarré souvent | Sans effet sur les passes périodiques : leur échéance est **persistée** et reprend là où elle en était. Une passe déjà due repart quelques minutes après le démarrage. Les jauges de fraîcheur sont réamorcées depuis la base ; les compteurs de runs, eux, repartent de zéro — normal pour Prometheus, `increase()` le gère. |
| La retention a purgé les snapshots | `keep_last` s'applique au dépôt principal ; le dépôt de copie, lui, n'est jamais purgé par SDB s'il est `append_only`. |

---

## 2. Incidents

### Une sauvegarde échoue

1. Lire la cause exacte — elle est enregistrée, pas seulement journalisée :

```bash
curl -s -H "Authorization: Bearer $JWT" \
  'http://127.0.0.1:8080/api/v1/backups/history?limit=5' | jq '.[] | {id,container_name,status,error_log}'
```

2. Les causes usuelles, dans l'ordre de fréquence :

| `error_log` contient | Cause | Geste |
|---|---|---|
| `pre-hook failed` | Le hook a rendu un code non nul. Par défaut c'est **fatal** : sauvegarder de l'incohérent est pire que ne pas sauvegarder. | Corriger le hook, ou passer sa politique à `continue` en connaissance de cause. |
| `partial` / `ErrPartial` | restic a rendu 3 : des fichiers sources étaient illisibles. En mode strict (défaut) le run échoue. | Identifier les fichiers (sockets, fichiers ouverts). Envisager un `stop_container` pour une sauvegarde à froid. |
| `repository is already locked` | Un worker précédent est mort en laissant un verrou. | [Débloquer un dépôt](#débloquer-un-dépôt-verrouillé) |
| `unable to open repository` | Endpoint injoignable, identifiants expirés, ou disque plein. | Tester l'accès avec le client restic ([ci-dessous](#parler-au-dépôt-sans-sdb)). |
| `CRITICAL: container ... could not be restarted` | **Priorité absolue** : la sauvegarde a arrêté un conteneur et n'a pas pu le relancer. | `docker start <conteneur>` immédiatement, puis diagnostiquer. |

3. Relancer une fois la cause traitée : bouton **Sauvegarder** dans l'interface,
   ou `POST /api/v1/schedules/:id/run` pour rejouer une planification.

### Un run finit en avertissement

`warning` = **la sauvegarde existe et est restaurable**, mais quelque chose
autour a raté. Ce n'est pas un échec, et ce n'est pas rien : le webhook alerte
aussi sur ce statut.

| Message | Signification |
|---|---|
| `secondary copy failed: ...` | Le snapshot n'existe qu'à **un** exemplaire. Voir [La copie secondaire a décroché](#la-copie-secondaire-a-décroché). |
| `retention failed: ...` | Les anciens snapshots n'ont pas été purgés : le dépôt va grossir. |
| `retention skipped: ... append-only` | Attendu sur un dépôt protégé : la politique est ignorée **bruyamment**, jamais appliquée en douce. |
| `post-hook failed` | Nettoyage applicatif raté. N'invalide pas le snapshot. |

### Une vérification échoue

C'est l'incident le plus important du document : une vérification qui échoue
dit qu'une **restauration réelle n'a pas abouti**. Toutes les sauvegardes de ce
dépôt sont suspectes jusqu'à preuve du contraire.

1. Retrouver le run — il est historisé comme une restauration, attribuée à
   `system:verification` :

```bash
curl -s -H "Authorization: Bearer $JWT" \
  'http://127.0.0.1:8080/api/v1/restores/history?limit=10' | jq '.[] | select(.triggered_by=="system:verification")'
```

2. Trancher entre les trois causes possibles :

- **Le dépôt est abîmé** → lancer un contrôle d'intégrité qui *relit les
  données* :
  `POST /api/v1/storage/:id/check` (résultat dans les logs).
  S'il échoue → [Le dépôt est corrompu](#le-dépôt-est-corrompu).
- **L'hôte manque de place** : la vérification écrit réellement dans un volume
  jetable `sdb-verify-*`. `docker system df`, puis
  `docker volume ls | grep sdb-verify` — un jetable oublié se supprime sans
  risque, SDB refuse de toucher à autre chose que ce préfixe.
- **Le snapshot n'a pas de chemin exploitable** (`has no /sdb/data/* path`) :
  snapshot créé hors SDB, ou dépôt partagé avec un autre outil.

3. Tant que la cause n'est pas traitée : **ne pas supprimer le conteneur ni le
   volume source**. Ce sont les seules données dont on est certain.

#### Déclencher une vérification sans attendre la passe

Un dépôt neuf n'a aucune preuve de restaurabilité avant sa première passe
planifiée — jusqu'à une semaine avec le réglage par défaut. Et une passe qui
vient d'échouer compte comme passée : la suivante est à un intervalle complet.

```bash
curl -s -X POST -H "Authorization: Bearer $JWT" \
  http://127.0.0.1:8080/api/v1/storage/<id>/verify        # 202
```

Bouton **« Prouver la restauration »** sur la page Stockage. À ne pas confondre
avec **« Contrôler »** (`/check`) : le premier restaure réellement le dernier
snapshot dans un volume jetable et recompare les empreintes, le second valide
la structure du dépôt et relit une fraction des données. Seul le premier prouve
qu'une sauvegarde se matérialise.

Le résultat part dans le flux d'événements et l'historique des restaurations
(`system:verification`). Un dépôt vide n'est pas un échec : il n'y a rien à
prouver.

#### Vérifier qu'une passe périodique s'arme réellement

Chaque boucle (vérification, contrôle d'intégrité, réconciliation) annonce au
démarrage son échéance, calculée depuis son **dernier passage** et non depuis
le boot :

```bash
docker compose logs sdb | grep 'periodic task armed'
# task=verification interval=168h0m0s first_pass_in=163h12m0s
```

`first_pass_in` égal à l'intervalle complet à chaque redémarrage signalerait
que l'échéance ne se souvient de rien. Les dates brutes se lisent dans la
table `maintenance_runs` :

```bash
VOL=$(docker volume ls -q -f name=sdb-data | head -1)
docker run --rm -v "$VOL:/data:ro" alpine:3 sh -c \
  'apk add --no-cache sqlite >/dev/null; sqlite3 "file:/data/sdb.db?mode=ro" "SELECT * FROM maintenance_runs;"'
```

### Le dépôt est corrompu

`restic check --read-data-subset` a signalé des paquets illisibles.

1. **Ne rien purger.** `forget`/`prune` sur un dépôt abîmé peut supprimer les
   dernières copies saines. Si le dépôt est `append_only`, SDB refuse déjà.
2. Mesurer l'étendue, hors SDB ([client restic direct](#parler-au-dépôt-sans-sdb)) :

```bash
restic -r <dépôt> check --read-data          # tout relire, c'est long
restic -r <dépôt> snapshots                  # que reste-t-il d'exploitable ?
```

3. Restaurer depuis la **copie secondaire** si elle existe : elle est un
   dépôt à part entière, avec ses propres paquets — c'est exactement le
   scénario qui justifie la règle 3-2-1.
4. Reconstruire : créer un **nouveau** stockage dans SDB (nouveau dépôt, nouveau
   mot de passe), y diriger les planifications, et ne retirer l'ancien qu'après
   avoir vérifié que le nouveau contient des snapshots restaurables.
5. `restic repair index` / `repair snapshots` existent, mais ils **suppriment**
   ce qu'ils ne peuvent pas réparer : ne les lancer qu'après avoir copié le
   dépôt ailleurs.

### La copie secondaire a décroché

```bash
curl -s -H "Authorization: Bearer $JWT" http://127.0.0.1:8080/api/v1/replication | jq
```

- `pending` > 0 : autant de snapshots n'existent qu'à **un** exemplaire.
- `lag_seconds` : ancienneté du plus ancien snapshot non copié.
- `error` : la copie ou sa source est injoignable — le message nomme laquelle.

Gestes :

1. La passe de réconciliation retente toute les `SDB_REPLICATION_INTERVAL`
   (6 h par défaut). Pour ne pas attendre :
   `POST /api/v1/storage/<id de la copie>/replicate` (admin, 202).
2. Si l'erreur mentionne une variable d'environnement en conflit
   (`... both define AWS_ACCESS_KEY_ID ...`) : la paire est inexploitable en
   l'état. restic partage les identifiants de backend entre un dépôt et sa
   source de copie ; il faut un compte commun aux deux dépôts, ou un backend
   différent pour l'un des deux.
3. Si le `pending` ne redescend pas après une passe manuelle réussie, vérifier
   qu'aucune sauvegarde n'écrit **directement** dans le dépôt de copie (SDB le
   refuse, un autre outil non).

### Une fenêtre planifiée a été manquée

L'échéance est tombée pendant un arrêt de SDB. Le rattrapage automatique est
**désactivé par défaut** : une sauvegarde peut arrêter son conteneur, et
rejouer en masse au redémarrage d'un hôte qui vient de tomber stopperait la
production au pire moment.

- Fenêtre importante → la rejouer à la main :
  `POST /api/v1/schedules/:id/run`.
- Arrêts fréquents et rattrapage souhaité → `SDB_SCHEDULE_CATCHUP=true`
  (une seule reprise par planification, jamais l'arriéré).

### SDB ne démarre plus

```bash
docker compose logs --tail=50 sdb
```

| Message | Cause | Geste |
|---|---|---|
| `SDB_MASTER_KEY ... must be set to at least 32 characters` | Secret absent ou tronqué | Vérifier le montage de `secrets/sdb_master_key`. |
| `decryption failed: wrong master key or corrupted data` | **La clé maître n'est pas celle qui a chiffré la base.** | Ne rien réécrire. Retrouver la bonne clé ; voir [Rotation des clés](#rotation-des-clés) si une rotation a été interrompue. |
| `applying migrations: ...` | Migration en échec | La base n'est pas modifiée (transaction). Restaurer une copie de `sdb.db` et signaler. |
| `docker daemon not reachable` | Socket non monté | Non bloquant au démarrage, mais aucune sauvegarde ne fonctionnera. |
| `SDB_DOCKER_HOST uses tcp://` | Démon distant sans mTLS complet | Fournir CA + certificat + clé, ou repasser au socket local. |

Le conteneur tourne mais rien ne se passe : vérifier qu'aucun worker n'est
resté bloqué —

```bash
docker ps --filter label=sdb.worker=true
```

Un worker orphelin se supprime sans risque (`docker rm -f`) : les volumes
sources y sont montés en **lecture seule**.

---

## 3. Restaurer

### Restaurer un volume

Deux modes, un seul endpoint :

- **En place** — `source_volume` omis : le volume est réécrit. Renseigner
  `stop_container` pour que SDB arrête l'application pendant l'opération et la
  redémarre ensuite quoi qu'il arrive. Une application qui écrit pendant la
  réécriture corrompt le volume.
- **Clonage** — `source_volume` ≠ `target_volume` : les données atterrissent
  dans un volume neuf, l'original n'est pas touché. **C'est le mode à préférer
  en situation de doute** : il ne détruit rien et permet de comparer.

```bash
curl -s -X POST -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' \
  -d '{"storage_id":1,"snapshot_id":"<id>","source_volume":"pgdata","target_volume":"pgdata_clone"}' \
  http://127.0.0.1:8080/api/v1/restores
```

Pour démarrer un service sur le clone sans toucher à l'original :
`GET /api/v1/restores/clone-compose?container_id=…&source_volume=…&target_volume=…`
rend un `docker-compose.yml` prêt à l'emploi.

La restauration est réservée au rôle **admin** : elle écrase de la production.

### Sinistre : le dépôt principal est perdu

La copie secondaire est un dépôt restic complet, avec son propre mot de passe :
elle se restaure **seule**, sans le dépôt principal.

1. Lister ses snapshots : `GET /api/v1/storage/<id de la copie>/snapshots`.
2. Restaurer en désignant `storage_id` = **l'id de la copie**, vers un volume
   neuf (clonage).
3. Reconstruire ensuite un dépôt principal : créer un nouveau stockage, puis
   une nouvelle copie secondaire qui le prend pour source. Le rattachement
   d'une copie est fixé à la création — on ne rebranche pas une copie
   existante, on en crée une.

### Sinistre : la base de SDB est perdue

`sdb.db` contient les mots de passe de tous les dépôts, chiffrés sous la clé
maître. Le perdre rend les dépôts **définitivement illisibles** — sauf si les
mots de passe ont été séquestrés à la création (l'API les affiche une fois, et
une seule).

Avec le mot de passe séquestré, le dépôt s'ouvre sans SDB :

#### Parler au dépôt sans SDB

```bash
# dépôt local, chemin vu par le démon Docker
docker run --rm -it \
  -e RESTIC_PASSWORD='<mot de passe séquestré>' \
  -v /mnt/backups/sdb:/repo \
  restic/restic:0.18.0 -r /repo snapshots

# restauration dans un volume Docker neuf
docker volume create rescue
docker run --rm \
  -e RESTIC_PASSWORD='<mot de passe séquestré>' \
  -v /mnt/backups/sdb:/repo -v rescue:/sdb/data/pgdata \
  restic/restic:0.18.0 -r /repo restore <snapshot> --target / --include /sdb/data/pgdata
```

Le chemin `--include` est celui **archivé** (`/sdb/data/<nom du volume
source>`), pas celui de la cible.

Pour un dépôt distant, remplacer `-r /repo` par l'URL et fournir les
identifiants du backend en variables (`AWS_ACCESS_KEY_ID`, etc.).

#### Débloquer un dépôt verrouillé

```bash
docker run --rm -e RESTIC_PASSWORD='…' -v /mnt/backups/sdb:/repo \
  restic/restic:0.18.0 -r /repo unlock
```

Ne le faire qu'après avoir vérifié qu'aucun worker ne tourne
(`docker ps --filter label=sdb.worker=true`).

**Sauvegarder `sdb-data` comme n'importe quel volume**, vers un dépôt dont le
mot de passe est détenu ailleurs : c'est ce qui rend ce scénario survivable.

---

## 4. Rotation des clés

### Clé maître (`SDB_MASTER_KEY`)

Elle chiffre les identifiants de backend et les mots de passe des dépôts. À
faire tourner si elle a pu fuiter, ou périodiquement.

L'opération est **hors ligne** : aucune route d'API ne la déclenche. Elle
réécrit tous les secrets d'un coup — un compte admin compromis n'a pas à
pouvoir la lancer.

```bash
# 1. arrêter le démon : il garde l'ancienne clé en mémoire
docker compose stop sdb

# 2. générer la nouvelle clé
openssl rand -hex 32 > secrets/sdb_master_key.new

# 3. rotation (SDB_MASTER_KEY reste la clé ACTUELLE)
docker compose run --rm \
  -e SDB_NEW_MASTER_KEY_FILE=/run/secrets/sdb_master_key_new \
  -v "$PWD/secrets/sdb_master_key.new:/run/secrets/sdb_master_key_new:ro" \
  sdb rotate-master-key

# 4. la nouvelle clé devient la clé courante
mv secrets/sdb_master_key.new secrets/sdb_master_key

# 5. redémarrer et vérifier
docker compose up -d sdb
docker compose logs --tail=30 sdb
curl -s -H "Authorization: Bearer $JWT" http://127.0.0.1:8080/api/v1/storage | jq '.[].name'
```

Garanties de la commande :

- une **copie consistante** de la base est écrite avant toute modification
  (`/data/sdb.db.pre-rotation-<horodatage>`) ;
- tout se passe dans une transaction : au premier échec, rien n'a bougé ;
- chaque secret ré-encrypté est **relu avec la nouvelle clé** et comparé à
  l'original avant le commit ;
- une clé courante erronée échoue au premier enregistrement, avant toute
  écriture.

**Après validation, détruire la copie pré-rotation** : elle contient tous les
secrets sous l'**ancienne** clé, c'est-à-dire précisément ce dont on cherchait
à se débarrasser.

L'image est distroless : ni shell ni `rm` dedans. Passer par un conteneur
jetable monté sur le même volume (le nom réel porte le préfixe du projet
compose, d'où la recherche) :

```bash
VOL=$(docker volume ls -q -f name=sdb-data | head -1)
docker run --rm -v "$VOL:/data" alpine:3 sh -c 'ls -l /data/sdb.db.pre-rotation-*'
docker run --rm -v "$VOL:/data" alpine:3 sh -c 'rm -f /data/sdb.db.pre-rotation-*'
```

Si le redémarrage échoue sur `decryption failed` : la clé fournie n'est pas la
nouvelle. Remettre la copie pré-rotation en place et recommencer.

### Secret JWT (`SDB_JWT_SECRET`)

Aucune migration de données : le changer invalide **toutes** les sessions.
Remplacer le secret et redémarrer ; tout le monde se reconnecte.

Pour révoquer une seule personne sans toucher au secret :
`POST /api/v1/users/:id/revoke-sessions`. Supprimer un compte, changer son rôle
ou son mot de passe révoque aussi ses jetons, immédiatement.

### Mot de passe d'un dépôt restic — limite assumée

**SDB ne sait pas faire tourner le mot de passe d'un dépôt existant.** Il est
immuable par conception : restic en dérive ses clés de chiffrement, et
l'API refuse de le modifier.

Si un mot de passe de dépôt a fuité :

1. Créer un **nouveau** stockage dans SDB (nouveau dépôt, nouveau mot de passe,
   séquestré) ;
2. rediriger les planifications vers lui ;
3. garder l'ancien dépôt en lecture le temps de sa rétention, puis le retirer.

Les données déjà écrites dans l'ancien dépôt restent lisibles avec le mot de
passe fuité : seule leur destruction — ou l'expiration de leur rétention —
ferme le sujet. `restic key add` / `key remove` permettent de changer les clés
du dépôt côté restic, mais SDB continuerait de présenter l'ancienne : ne pas
l'utiliser sans recréer la configuration dans SDB.

---

## 5. Entretien courant

### Mettre SDB à jour

```bash
docker compose pull && docker compose up -d --build sdb
docker compose logs --tail=30 sdb
```

Les migrations de schéma s'appliquent au démarrage, chacune dans une
transaction avec contrôle d'intégrité référentielle avant commit. En cas
d'échec, la base n'est pas modifiée et le démon refuse de démarrer — c'est
voulu.

Une mise à jour majeure peut invalider les jetons existants (c'était le cas de
la révocation de session) : prévoir une reconnexion.

### Activer la copie secondaire après coup

SDB fonctionne sans seconde copie — c'est le mode par défaut, et il est
signalé à chaque démarrage. L'activer plus tard ne demande **aucune
reconfiguration** de ce qui existe : le lien est porté par la copie, pas par
la source.

1. Créer un stockage en désignant son **dépôt source** (« Ajouter une copie
   secondaire » sur la page Stockage, ou `copy_of_storage_id` à la création
   par l'API). De préférence sur un **autre support** que le dépôt principal :
   c'est tout l'objet de la règle.
2. Le dépôt est initialisé depuis sa source (`--copy-chunker-params`), puis
   **les snapshots déjà présents sont recopiés immédiatement**, en tâche de
   fond. Activer la seconde copie ne protège donc pas que les sauvegardes à
   venir : l'historique part aussi.
3. Suivre l'avancement — `pending` doit descendre à 0 :

```bash
curl -s -H "Authorization: Bearer $JWT" http://127.0.0.1:8080/api/v1/replication | jq
```

La première recopie re-téléverse tout (restic ré-encrypte) : sur un gros dépôt
distant, compter en heures. Un arrêt de SDB pendant l'opération est sans
conséquence — `restic copy` saute ce qui est déjà copié et la passe de
réconciliation reprend le reste.

Points d'attention :

- **Le rattachement ne se change plus** après création. Se tromper de source
  se corrige en créant une autre copie, pas en rebranchant celle-ci.
- **Un dépôt de copie refuse les sauvegardes directes** : il disparaît des
  sélecteurs de sauvegarde et de planification, et reste dans celui de
  restauration.
- Le marquer **append-only** est cohérent avec son rôle : SDB n'y appliquera
  jamais de rétention, la copie garde donc l'historique complet même si le
  principal est purgé.
- Si le dépôt de destination **existe déjà** et contient des données, il est
  réutilisé tel quel : il ne peut simplement plus hériter des paramètres de
  découpage de la source (ils sont fixés à l'initialisation), ce qui peut
  coûter de l'espace en double.

### Exercice de restauration (à faire, pas à lire)

Les vérifications automatiques prouvent qu'un snapshot se matérialise ; elles
ne prouvent pas que **l'équipe** sait restaurer. Une fois par trimestre :

1. Cloner un volume de production vers `<volume>_drill` depuis l'interface.
2. Démarrer un service dessus avec le compose rendu par `/restores/clone-compose`.
3. Vérifier les données applicatives (pas seulement la présence des fichiers).
4. Chronométrer, comparer au RTO annoncé, mettre le chiffre à jour.
5. Supprimer le clone.

Rien de ce qui précède ne touche la production.

### Contrôle trimestriel

- [ ] `SDB_VERIFY_INTERVAL` et `SDB_ALERT_WEBHOOK` sont renseignés
- [ ] Une copie secondaire existe et `pending` vaut 0
- [ ] `sdb-data` est lui-même sauvegardé, vers un dépôt dont le mot de passe
      est détenu ailleurs
- [ ] Les mots de passe de dépôt sont dans le gestionnaire de secrets
- [ ] L'exercice de restauration a été fait, le RTO relevé
- [ ] Les règles d'alerte de `deploy/prometheus/sdb-alerts.yml` sont chargées
- [ ] L'immuabilité **côté serveur** est active (`rest-server --append-only`,
      S3 Object Lock) : le verrou `append_only` de SDB retire SDB des vecteurs
      de destruction, il ne protège pas d'un accès direct au dépôt

---

## 6. Ce que SDB ne garantit pas

Assumé et documenté, pour qu'aucune de ces limites ne soit découverte pendant
un incident :

- **Pas de haute disponibilité.** SDB arrêté = aucune sauvegarde. Les fenêtres
  manquées sont détectées (`sdb_schedule_missed_runs_total`), pas évitées.
- **Le socket Docker est monté en direct**, sans proxy limitant la surface
  d'API : SDB est root-équivalent sur son hôte.
- **La configuration n'est pas déclarative** : dépôts et planifications
  n'existent que dans `sdb.db`, ils ne se reconstruisent pas depuis git.
- **Le mot de passe d'un dépôt ne tourne pas** (ci-dessus).
- **`append_only` est un verrou applicatif**, pas de l'immuabilité.
