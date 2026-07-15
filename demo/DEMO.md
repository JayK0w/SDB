# 🎬 Démo visuelle SDB — scénario de présentation

Objectif : montrer en direct qu'un snapshot SDB capture l'état d'un volume
et qu'une restauration le ramène, avec un résultat visible dans le
navigateur.

## Décor (à préparer AVANT la présentation)

```sh
# depuis la racine du projet — SDB doit déjà tourner (docker compose up -d)
docker volume create demo-web-data
docker run -d --name demo-web -v demo-web-data:/usr/share/nginx/html -p 127.0.0.1:8081:80 nginx:alpine
docker cp demo/index-v1.html demo-web:/usr/share/nginx/html/v1.html
docker cp demo/index-v2.html demo-web:/usr/share/nginx/html/v2.html
docker exec demo-web sh -c "cp /usr/share/nginx/html/v1.html /usr/share/nginx/html/index.html"
```

Deux onglets ouverts :
- **http://127.0.0.1:8081** → la « super app » (page VERTE v1.0)
- **http://127.0.0.1:8080** → le dashboard SDB, connecté

## Déroulé (5 minutes)

1. **Montrer la page verte v1.0** — « voici l'application et ses données,
   dans le volume Docker demo-web-data ».

2. **Snapshot** — dans SDB : Tableau de bord → ligne `demo-web` →
   **Sauvegarder** → destination `test-local` → Lancer.
   → barre de progression en direct, toast de succès, run visible dans
   l'Historique avec son ID de snapshot.

3. **L'incident** — « un mauvais déploiement écrase les données » :
   ```sh
   docker exec demo-web sh -c "cp /usr/share/nginx/html/v2.html /usr/share/nginx/html/index.html"
   ```
   Rafraîchir l'onglet 8081 → **page ROUGE v2.0 « données corrompues »**.

4. **Restauration** — dans SDB : `demo-web` → **Restaurer** → le snapshot
   de l'étape 2 est présélectionné → volume `demo-web-data` → laisser
   « arrêter le conteneur » coché → Restaurer.
   → nginx s'arrête, restic réécrit le volume, nginx repart (~10 s),
   le tout tracé dans Historique → onglet Restaurations.

5. **Rafraîchir l'onglet 8081** → 🎉 **la page VERTE v1.0 est revenue**.

## Remise à zéro (rejouer la démo)

```sh
docker exec demo-web sh -c "cp /usr/share/nginx/html/v1.html /usr/share/nginx/html/index.html"
```
(ou refaire une restauration depuis SDB — c'est encore plus démonstratif)

## Tout nettoyer après la présentation

```sh
docker rm -f demo-web && docker volume rm demo-web-data
```

## Si quelque chose coince

- Page 8081 injoignable → `docker start demo-web`
- SDB injoignable → `docker compose up -d` puis vérifier `docker compose logs sdb`
- Pas de snapshot proposé dans la modale → vérifier que la sauvegarde de
  l'étape 2 est bien en « Succès » dans l'Historique
