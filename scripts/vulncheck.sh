#!/usr/bin/env bash
# Analyse de vulnérabilités avec exceptions explicites.
#
# govulncheck seul ne sait pas ignorer un avis. Or trois vulnérabilités du
# démon Docker n'ont aucun correctif disponible et ne sont pas atteignables
# par l'usage que SDB fait du client. Sans mécanisme d'exception, le gate
# serait rouge en permanence — donc ignoré, donc inutile.
#
# Ce script échoue sur toute vulnérabilité ABSENTE de .govulncheck-allow.
# Une nouvelle CVE casse le build ; les exceptions restent visibles, datées
# et justifiées dans ce fichier.
set -uo pipefail

cd "$(dirname "$0")/.."
ALLOW_FILE=".govulncheck-allow"

# On invoque le BINAIRE, pas `go run` : ce dernier écrase le code de sortie
# de l'outil (3 = vulnérabilités trouvées) par un 1 générique, ce qui rend
# impossible de distinguer « vulnérabilités » de « l'outil a planté ».
BIN="$(command -v govulncheck || true)"
if [ -z "$BIN" ]; then
    BIN="$(go env GOPATH)/bin/govulncheck"
    if [ ! -x "$BIN" ]; then
        echo "== installation de govulncheck =="
        go install golang.org/x/vuln/cmd/govulncheck@latest || exit 1
    fi
fi

echo "== govulncheck =="
OUT="$("$BIN" ./... 2>&1)"
STATUS=$?
if [ "$STATUS" -ne 0 ] && [ "$STATUS" -ne 3 ]; then
    printf '%s\n' "$OUT"
    echo "ECHEC : govulncheck n'a pas pu s'exécuter (statut $STATUS)"
    exit 1
fi

# les IDs listés en "Vulnerability #N:" sont exactement l'ensemble ATTEIGNABLE
FOUND="$(printf '%s\n' "$OUT" | sed -n 's/^Vulnerability #[0-9]*: \(GO-[0-9]*-[0-9]*\).*/\1/p' | sort -u)"

ALLOWED=""
if [ -f "$ALLOW_FILE" ]; then
    ALLOWED="$(sed 's/#.*//' "$ALLOW_FILE" | tr -d ' \t' | grep -E '^GO-[0-9]+-[0-9]+$' | sort -u)"
fi

if [ -z "$FOUND" ]; then
    echo "OK : aucune vulnérabilité atteignable."
    exit 0
fi

UNEXPECTED="$(comm -23 <(printf '%s\n' "$FOUND") <(printf '%s\n' "$ALLOWED"))"
ACCEPTED="$(comm -12 <(printf '%s\n' "$FOUND") <(printf '%s\n' "$ALLOWED"))"
STALE="$(comm -13 <(printf '%s\n' "$FOUND") <(printf '%s\n' "$ALLOWED"))"

if [ -n "$ACCEPTED" ]; then
    echo
    echo "Acceptées explicitement (cf. $ALLOW_FILE) :"
    printf '  %s\n' $ACCEPTED
    # la date de revue doit être vue à chaque exécution, sinon la dette dort
    grep -E '^# (Dernière|Prochaine) revue' "$ALLOW_FILE" | sed 's/^# /  /'
fi

# une exception devenue inutile est du bruit qui masque les vraies
if [ -n "$STALE" ]; then
    echo
    echo "Exceptions devenues inutiles — à retirer de $ALLOW_FILE :"
    printf '  %s\n' $STALE
fi

if [ -n "$UNEXPECTED" ]; then
    echo
    echo "ECHEC : vulnérabilité(s) atteignable(s) non acceptée(s) :"
    printf '  %s\n' $UNEXPECTED
    echo
    # detail complet uniquement en cas d'echec : la sortie est enorme
    printf '%s\n' "$OUT"
    exit 1
fi

echo
echo "OK : aucune vulnérabilité atteignable hors exceptions déclarées."
