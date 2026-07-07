package httpapi

import (
	"context"
	"log/slog"
	"time"

	"github.com/gorilla/websocket"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

const (
	// writeWait bounds a single message write to a client.
	writeWait = 10 * time.Second
	// pongWait is how long a client may stay silent before being dropped;
	// pings are sent at pingPeriod (< pongWait) to keep it alive.
	pongWait   = 60 * time.Second
	pingPeriod = 54 * time.Second
	// maxMessageSize caps inbound frames: clients only listen.
	maxMessageSize = 512
	// clientBuffer is each client's send queue; a client lagging behind
	// this many events is dropped instead of slowing everyone down.
	clientBuffer = 64
)

// Hub fans ProgressEvents out to every connected WebSocket client. It
// implements domain.EventPublisher: Publish never blocks — when the hub
// or one client cannot keep up, the event (or the client) is dropped
// rather than stalling a running backup goroutine.
type Hub struct {
	logger     *slog.Logger
	register   chan *client
	unregister chan *client
	broadcast  chan domain.ProgressEvent
	clients    map[*client]struct{}
	done       chan struct{} // closed when Run exits
}

func NewHub(logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	return &Hub{
		logger:     logger,
		register:   make(chan *client),
		unregister: make(chan *client),
		broadcast:  make(chan domain.ProgressEvent, 256),
		clients:    map[*client]struct{}{},
		done:       make(chan struct{}),
	}
}

var _ domain.EventPublisher = (*Hub)(nil)

// Run owns the clients map; all mutations happen on this single goroutine,
// which is what makes the hub lock-free and non-blocking for publishers.
func (h *Hub) Run(ctx context.Context) {
	defer close(h.done)
	for {
		select {
		case <-ctx.Done():
			for c := range h.clients {
				close(c.send)
			}
			clear(h.clients)
			return
		case c := <-h.register:
			h.clients[c] = struct{}{}
		case c := <-h.unregister:
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
		case ev := <-h.broadcast:
			for c := range h.clients {
				select {
				case c.send <- ev:
				default:
					// Slow consumer: drop the client, never the backup.
					h.logger.Warn("dropping slow websocket client")
					delete(h.clients, c)
					close(c.send)
				}
			}
		}
	}
}

// Publish implements domain.EventPublisher and never blocks: if the hub
// itself is congested the event is dropped (the authoritative state lives
// in backups_history; the stream is best-effort telemetry).
func (h *Hub) Publish(ev domain.ProgressEvent) {
	select {
	case h.broadcast <- ev:
	default:
	}
}

func (h *Hub) add(c *client) {
	select {
	case h.register <- c:
	case <-h.done:
		close(c.send)
	}
}

func (h *Hub) remove(c *client) {
	select {
	case h.unregister <- c:
	case <-h.done:
	}
}

// client is one WebSocket subscriber with its buffered send queue.
type client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan domain.ProgressEvent
}

// writePump serialises events to the socket and keeps the connection
// alive with pings. It is the only goroutine writing to the connection.
func (c *client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case ev, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, ""))
				return
			}
			if err := c.conn.WriteJSON(ev); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// readPump drains (and discards) inbound frames so pongs are processed
// and closed connections are detected promptly.
func (c *client) readPump() {
	defer func() {
		c.hub.remove(c)
		c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}
