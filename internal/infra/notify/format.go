package notify

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Format : schéma de la charge utile postée.
//
// La structure native de SDB est du JSON arbitraire : parfaite pour un
// récepteur générique (Alertmanager, n8n, endpoint maison), mais rejetée par
// Slack qui impose son propre schéma. Plutôt que d'imposer un adaptateur
// intermédiaire à l'exploitant, le format est un réglage.
type Format string

const (
	// FormatSDB : structure native, JSON arbitraire.
	FormatSDB Format = "sdb"
	// FormatSlack : schéma « Incoming Webhook » de Slack. Compatible aussi
	// avec Mattermost et Rocket.Chat, qui l'ont repris.
	FormatSlack Format = "slack"
)

// ParseFormat : valide un format venu de la configuration.
//
// Une valeur inconnue est une ERREUR, jamais un repli silencieux sur le
// format natif : une faute de frappe (`slak`) produirait des alertes que
// Slack rejette, et l'exploitant croirait être notifié.
func ParseFormat(s string) (Format, error) {
	switch Format(strings.ToLower(strings.TrimSpace(s))) {
	case "", FormatSDB:
		return FormatSDB, nil
	case FormatSlack:
		return FormatSlack, nil
	default:
		return "", fmt.Errorf("unknown alert format %q (want %q or %q)", s, FormatSDB, FormatSlack)
	}
}

// couleurs de la barre latérale Slack, par sévérité
const (
	colorFailed  = "#d64541"
	colorWarning = "#e6a23c"
)

type slackPayload struct {
	Text        string            `json:"text"`
	Attachments []slackAttachment `json:"attachments,omitempty"`
}

type slackAttachment struct {
	Color    string   `json:"color"`
	Text     string   `json:"text"`
	Footer   string   `json:"footer"`
	Ts       int64    `json:"ts"`
	MrkdwnIn []string `json:"mrkdwn_in"`
}

// encode : rend la charge utile selon le format configuré.
func encode(a alert, f Format) ([]byte, error) {
	if f == FormatSlack {
		return json.Marshal(toSlack(a))
	}
	return json.Marshal(a)
}

func toSlack(a alert) slackPayload {
	kind := "Sauvegarde"
	ref := ""
	switch {
	case a.RestoreID != 0:
		kind = "Restauration"
		ref = fmt.Sprintf(" #%d", a.RestoreID)
	case a.BackupID != 0:
		ref = fmt.Sprintf(" #%d", a.BackupID)
	}

	icon, color := ":rotating_light:", colorFailed
	if a.Status == "warning" {
		icon, color = ":warning:", colorWarning
	}

	title := fmt.Sprintf("%s *%s%s — %s*", icon, kind, ref, escapeSlack(a.Status))
	if a.Container != "" {
		title += fmt.Sprintf(" · `%s`", escapeSlack(a.Container))
	}

	body := escapeSlack(strings.TrimSpace(a.Message))
	if body == "" {
		body = "_aucun detail fourni_"
	}

	return slackPayload{
		Text: title,
		Attachments: []slackAttachment{{
			Color:    color,
			Text:     body,
			Footer:   "SDB",
			Ts:       a.Time.Unix(),
			MrkdwnIn: []string{"text"},
		}},
	}
}

// escapeSlack : Slack réserve &, < et > dans son mrkdwn. Sans échappement,
// une sortie restic contenant `<nil>` ou une comparaison tronque le message
// à l'affichage. Ordre imposé : & en premier, sinon on ré-échappe les
// entités qu'on vient de produire.
func escapeSlack(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
