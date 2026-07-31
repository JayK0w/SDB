package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestWebhookPostsOnTerminalFailure(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	done := make(chan struct{}, 4)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		done <- struct{}{}
	}))
	defer srv.Close()

	wh := New(srv.URL, time.Second, discardLogger())
	wh.Publish(domain.ProgressEvent{
		BackupID: 42, Container: "postgres", Type: domain.EventStatus,
		Status: domain.BackupFailed, Message: "restic exited 1", Time: time.Now().UTC(),
	})

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("webhook was never called")
	}
	if err := wh.Close(context.Background()); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	var a alert
	if err := json.Unmarshal(bodies[0], &a); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if a.Event != "backup.failed" || a.BackupID != 42 || a.Container != "postgres" {
		t.Fatalf("unexpected payload: %+v", a)
	}
}

// Seules les fins en incident partent : le flux contient des milliers
// d'événements de progression, les relayer noierait l'alerte.
func TestWebhookIgnoresNonTerminalAndSuccess(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
	}))
	defer srv.Close()

	wh := New(srv.URL, time.Second, discardLogger())
	wh.Publish(domain.ProgressEvent{BackupID: 1, Type: domain.EventProgress, Percent: 0.5})
	wh.Publish(domain.ProgressEvent{BackupID: 1, Type: domain.EventLog, Message: "scanning"})
	wh.Publish(domain.ProgressEvent{BackupID: 1, Type: domain.EventStatus, Status: domain.BackupRunning})
	wh.Publish(domain.ProgressEvent{BackupID: 1, Type: domain.EventStatus, Status: domain.BackupSuccess})
	if err := wh.Close(context.Background()); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 0 {
		t.Fatalf("webhook called %d time(s) for non-incident events", calls)
	}
}

// Contrat domain.EventPublisher : Publish ne doit JAMAIS bloquer. Un webhook
// injoignable ne doit pas figer le pipeline de sauvegarde.
func TestWebhookPublishNeverBlocksOnHangingEndpoint(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release // endpoint gelé
	}))
	defer srv.Close()
	defer close(release)

	wh := New(srv.URL, 50*time.Millisecond, discardLogger())

	done := make(chan struct{})
	go func() {
		// bien plus que la profondeur de file : force le chemin d'abandon
		for i := 0; i < queueSize*4; i++ {
			wh.Publish(domain.ProgressEvent{
				BackupID: int64(i), Type: domain.EventStatus, Status: domain.BackupFailed,
			})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Publish blocked; the backup pipeline would have stalled")
	}
	if wh.Dropped() == 0 {
		t.Fatal("expected alerts to be dropped rather than queued without bound")
	}
}

func TestWebhookDisabledWithoutURL(t *testing.T) {
	var wh *Webhook = New("", time.Second, discardLogger())
	if wh != nil {
		t.Fatal("empty url must yield a nil webhook")
	}
	// le receveur nil doit rester utilisable : l'appelant ne teste pas
	wh.Publish(domain.ProgressEvent{Type: domain.EventStatus, Status: domain.BackupFailed})
	if err := wh.Close(context.Background()); err != nil {
		t.Fatalf("Close() on nil webhook: %v", err)
	}
	if wh.Dropped() != 0 {
		t.Fatal("nil webhook should report no drops")
	}
}
