// Package notify : alertes sortantes sur incident. Une sauvegarde qui échoue
// sans que personne ne l'apprenne est le pire mode de défaillance d'un
// système de sauvegarde — l'exploitant croit être couvert jusqu'au jour où
// il tente une restauration.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// queueSize : profondeur du tampon d'alertes. Volontairement petit — une
// rafale d'échecs n'a pas besoin d'être notifiée intégralement, elle a
// besoin d'être notifiée UNE fois et vite.
const queueSize = 64

// Webhook : publisher qui poste un JSON sur incident terminal.
//
// Contrat domain.EventPublisher : Publish ne bloque JAMAIS. L'envoi HTTP se
// fait dans une goroutine dédiée ; si la file est pleine, l'alerte est
// abandonnée et comptabilisée plutôt que de ralentir une sauvegarde.
type Webhook struct {
	url    string
	client *http.Client
	logger *slog.Logger

	queue chan alert
	wg    sync.WaitGroup

	closeOnce sync.Once

	mu      sync.Mutex
	dropped int64
}

var _ domain.EventPublisher = (*Webhook)(nil)

// alert : charge utile envoyée. Volontairement pauvre — ni identifiants, ni
// endpoint de dépôt, ni sortie brute de restic : la destination du webhook
// est un tiers (Slack, Alertmanager, n8n) qu'il ne faut pas transformer en
// canal d'exfiltration.
type alert struct {
	Event     string    `json:"event"`
	Status    string    `json:"status"`
	BackupID  int64     `json:"backup_id,omitempty"`
	RestoreID int64     `json:"restore_id,omitempty"`
	Container string    `json:"container,omitempty"`
	Message   string    `json:"message,omitempty"`
	Time      time.Time `json:"time"`
	Source    string    `json:"source"`
}

// New : webhook prêt à publier. url vide = nil, l'appelant l'omet du
// MultiPublisher.
func New(url string, timeout time.Duration, logger *slog.Logger) *Webhook {
	if url == "" {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	w := &Webhook{
		url:    url,
		client: &http.Client{Timeout: timeout},
		logger: logger,
		queue:  make(chan alert, queueSize),
	}
	w.wg.Add(1)
	go w.loop()
	return w
}

// Publish : ne retient que les transitions terminales en échec. Un nil
// receiver est un no-op, pour que l'appelant n'ait pas à tester.
func (w *Webhook) Publish(ev domain.ProgressEvent) {
	if w == nil || ev.Type != domain.EventStatus {
		return
	}
	if ev.Status != domain.BackupFailed && ev.Status != domain.BackupWarning {
		return
	}

	kind := "backup"
	if ev.RestoreID != 0 {
		kind = "restore"
	}
	a := alert{
		Event:     kind + "." + string(ev.Status),
		Status:    string(ev.Status),
		BackupID:  ev.BackupID,
		RestoreID: ev.RestoreID,
		Container: ev.Container,
		Message:   ev.Message,
		Time:      ev.Time,
		Source:    "sdb",
	}

	select {
	case w.queue <- a:
	default:
		// file pleine : on abandonne. Bloquer ici gèlerait le pipeline de
		// sauvegarde sur une indisponibilité du webhook.
		w.mu.Lock()
		w.dropped++
		n := w.dropped
		w.mu.Unlock()
		w.logger.Warn("alert dropped, webhook queue is full", "dropped_total", n)
	}
}

func (w *Webhook) loop() {
	defer w.wg.Done()
	for a := range w.queue {
		w.post(a)
	}
}

func (w *Webhook) post(a alert) {
	body, err := json.Marshal(a)
	if err != nil {
		w.logger.Error("encoding alert", "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), w.client.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		w.logger.Error("building alert request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "sdb-alerts")

	resp, err := w.client.Do(req)
	if err != nil {
		// l'URL peut contenir un jeton : ne jamais la logger
		w.logger.Error("delivering alert", "error", redactURL(err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		w.logger.Error("alert rejected by webhook", "status", resp.StatusCode)
	}
}

// Close : vide la file puis rend la main. Idempotent.
func (w *Webhook) Close(ctx context.Context) error {
	if w == nil {
		return nil
	}
	w.closeOnce.Do(func() { close(w.queue) })

	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Dropped : alertes abandonnées faute de place, pour les tests et le
// diagnostic.
func (w *Webhook) Dropped() int64 {
	if w == nil {
		return 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dropped
}

// redactURL : les erreurs de net/http contiennent l'URL complète, jeton
// compris. On ne remonte que la nature de l'échec.
func redactURL(err error) string {
	return fmt.Sprintf("%T", err)
}
