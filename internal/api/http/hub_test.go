package httpapi

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func recvEvent(t *testing.T, ch chan domain.ProgressEvent) (domain.ProgressEvent, bool) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		return ev, ok
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
		return domain.ProgressEvent{}, false
	}
}

func TestHubBroadcastsToClients(t *testing.T) {
	hub := NewHub(testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	a := &client{hub: hub, send: make(chan domain.ProgressEvent, 8)}
	b := &client{hub: hub, send: make(chan domain.ProgressEvent, 8)}
	hub.add(a)
	hub.add(b)

	hub.Publish(domain.ProgressEvent{BackupID: 1, Type: domain.EventLog, Message: "hello"})

	for _, cl := range []*client{a, b} {
		ev, ok := recvEvent(t, cl.send)
		if !ok || ev.BackupID != 1 || ev.Message != "hello" {
			t.Fatalf("client received %+v (ok=%v)", ev, ok)
		}
	}
}

func TestHubDropsSlowConsumerWithoutBlocking(t *testing.T) {
	hub := NewHub(testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	fast := &client{hub: hub, send: make(chan domain.ProgressEvent, 8)}
	slow := &client{hub: hub, send: make(chan domain.ProgressEvent, 1)} // sature apres un evenement
	hub.add(fast)
	hub.add(slow)

	hub.Publish(domain.ProgressEvent{BackupID: 1, Type: domain.EventLog, Message: "one"})
	hub.Publish(domain.ProgressEvent{BackupID: 1, Type: domain.EventLog, Message: "two"})

	// le client rapide recoit les deux evenements
	if ev, _ := recvEvent(t, fast.send); ev.Message != "one" {
		t.Fatalf("fast client first event = %q, want one", ev.Message)
	}
	if ev, _ := recvEvent(t, fast.send); ev.Message != "two" {
		t.Fatalf("fast client second event = %q, want two", ev.Message)
	}

	// le client lent a recu le premier puis a ete evince : son canal
	// doit etre ferme, pas laisse bloquer le hub
	if ev, ok := recvEvent(t, slow.send); !ok || ev.Message != "one" {
		t.Fatalf("slow client first event = %+v (ok=%v)", ev, ok)
	}
	if _, ok := recvEvent(t, slow.send); ok {
		t.Fatal("slow client channel should be closed after being dropped")
	}
}

func TestHubUnregisterClosesClient(t *testing.T) {
	hub := NewHub(testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	c := &client{hub: hub, send: make(chan domain.ProgressEvent, 8)}
	hub.add(c)
	hub.remove(c)

	if _, ok := recvEvent(t, c.send); ok {
		t.Fatal("send channel should be closed after unregister")
	}
	// publier ensuite ne doit ni paniquer ni bloquer
	hub.Publish(domain.ProgressEvent{Type: domain.EventLog, Message: "after"})
}

func TestHubShutdownClosesEverything(t *testing.T) {
	hub := NewHub(testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)

	c := &client{hub: hub, send: make(chan domain.ProgressEvent, 8)}
	hub.add(c)
	cancel()

	if _, ok := recvEvent(t, c.send); ok {
		t.Fatal("send channel should be closed on hub shutdown")
	}
	// add/remove apres arret : pas de deadlock
	late := &client{hub: hub, send: make(chan domain.ProgressEvent, 1)}
	hub.add(late)
	hub.remove(late)
	hub.Publish(domain.ProgressEvent{Type: domain.EventLog})
}
