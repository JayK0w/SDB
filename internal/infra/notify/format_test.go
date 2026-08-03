package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// Une valeur inconnue doit ETRE UNE ERREUR : repliee silencieusement sur le
// format natif, une faute de frappe produirait des alertes que Slack rejette
// et l'exploitant se croirait notifie.
func TestParseFormatRejectsUnknownValue(t *testing.T) {
	for _, bad := range []string{"slak", "teams", "json", "SLACK-"} {
		if _, err := ParseFormat(bad); err == nil {
			t.Fatalf("ParseFormat(%q) should have failed", bad)
		}
	}
}

func TestParseFormatAcceptsKnownValues(t *testing.T) {
	cases := map[string]Format{
		"":        FormatSDB, // non configure = natif
		"sdb":     FormatSDB,
		"slack":   FormatSlack,
		"  SLACK": FormatSlack, // tolerant a la casse et aux espaces
	}
	for in, want := range cases {
		got, err := ParseFormat(in)
		if err != nil {
			t.Fatalf("ParseFormat(%q) error: %v", in, err)
		}
		if got != want {
			t.Fatalf("ParseFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

// Le schema Slack impose un champ `text` a la racine : c'est precisement ce
// que la charge utile native n'a pas, d'ou le rejet en 400.
func TestSlackPayloadCarriesTextAtRoot(t *testing.T) {
	raw, err := encode(alert{
		Event: "backup.failed", Status: "failed", BackupID: 42,
		Container: "postgres", Message: "restic exited 1",
		Time: time.Unix(1700000000, 0).UTC(), Source: "sdb",
	}, FormatSlack)
	if err != nil {
		t.Fatalf("encode() error: %v", err)
	}

	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	text, _ := p["text"].(string)
	if text == "" {
		t.Fatalf("slack payload must carry a non-empty root `text`: %s", raw)
	}
	for _, want := range []string{"Sauvegarde", "#42", "postgres", "failed"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text %q missing %q", text, want)
		}
	}
	// le detail vit dans l'attachement, avec la barre de severite
	atts, _ := p["attachments"].([]any)
	if len(atts) != 1 {
		t.Fatalf("expected one attachment, got %v", p["attachments"])
	}
	a := atts[0].(map[string]any)
	if a["color"] != colorFailed {
		t.Fatalf("color = %v, want %s for a failure", a["color"], colorFailed)
	}
	if !strings.Contains(a["text"].(string), "restic exited 1") {
		t.Fatalf("attachment lost the message: %v", a["text"])
	}
}

func TestSlackPayloadUsesWarningColour(t *testing.T) {
	raw, _ := encode(alert{Status: "warning", BackupID: 1, Time: time.Now()}, FormatSlack)
	var p slackPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.Attachments[0].Color != colorWarning {
		t.Fatalf("color = %s, want %s for a warning", p.Attachments[0].Color, colorWarning)
	}
}

func TestSlackPayloadNamesRestoresCorrectly(t *testing.T) {
	raw, _ := encode(alert{Status: "failed", RestoreID: 7, Time: time.Now()}, FormatSlack)
	var p slackPayload
	json.Unmarshal(raw, &p)
	if !strings.Contains(p.Text, "Restauration") || !strings.Contains(p.Text, "#7") {
		t.Fatalf("a restore alert must say so: %q", p.Text)
	}
}

// Slack reserve &, < et > : une sortie restic contenant `<nil>` tronquerait
// le message a l'affichage.
func TestSlackEscapesReservedCharacters(t *testing.T) {
	raw, _ := encode(alert{
		Status: "failed", BackupID: 1, Message: "cmp failed: a<b && c>d <nil>", Time: time.Now(),
	}, FormatSlack)
	var p slackPayload
	json.Unmarshal(raw, &p)

	body := p.Attachments[0].Text
	for _, bad := range []string{"<b", "c>d", "<nil>"} {
		if strings.Contains(body, bad) {
			t.Fatalf("unescaped %q in %q", bad, body)
		}
	}
	// & doit etre echappe une seule fois, pas en cascade
	if strings.Contains(body, "&amp;amp;") {
		t.Fatalf("double-escaped ampersand in %q", body)
	}
	if !strings.Contains(body, "&lt;nil&gt;") {
		t.Fatalf("expected escaped <nil> in %q", body)
	}
}

// Le format natif ne doit pas bouger : c'est le defaut, et des recepteurs
// generiques en dependent.
func TestNativeFormatUnchanged(t *testing.T) {
	raw, err := encode(alert{
		Event: "backup.failed", Status: "failed", BackupID: 42,
		Container: "postgres", Message: "boom", Time: time.Now(), Source: "sdb",
	}, FormatSDB)
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	json.Unmarshal(raw, &p)
	for _, k := range []string{"event", "status", "backup_id", "container", "message", "source"} {
		if _, ok := p[k]; !ok {
			t.Fatalf("native payload lost field %q: %s", k, raw)
		}
	}
	if _, leaked := p["attachments"]; leaked {
		t.Fatal("native payload must not carry slack fields")
	}
}

// Verification de bout en bout : le format configure atteint bien le reseau.
func TestWebhookPostsConfiguredFormat(t *testing.T) {
	var mu sync.Mutex
	var got []byte
	done := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = b
		mu.Unlock()
		done <- struct{}{}
	}))
	defer srv.Close()

	wh := New(srv.URL, time.Second, discardLogger(), WithFormat(FormatSlack))
	wh.Publish(domain.ProgressEvent{
		BackupID: 5, Container: "redis", Type: domain.EventStatus,
		Status: domain.BackupFailed, Message: "oom", Time: time.Now().UTC(),
	})
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("webhook never called")
	}
	if err := wh.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	var p map[string]any
	if err := json.Unmarshal(got, &p); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if _, ok := p["text"]; !ok {
		t.Fatalf("configured slack format did not reach the wire: %s", got)
	}
}
