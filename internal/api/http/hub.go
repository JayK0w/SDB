// Package httpapi : couche de livraison — API REST (/api/v1, Gin) et hub
// WebSocket. Traduit le transport (JSON, codes HTTP, JWT) vers les usecases.
package httpapi

import (
	"context"
	"log/slog"
	"time"

	"github.com/gorilla/websocket"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

const (
	writeWait      = 10 * time.Second // borne une écriture
	pongWait       = 60 * time.Second // silence max avant déconnexion
	pingPeriod     = 54 * time.Second // < pongWait
	maxMessageSize = 512              // les clients n'envoient rien
	clientBuffer   = 64               // file par client avant éviction
)

// Hub : diffuse les ProgressEvents à tous les clients WebSocket.
// Publish ne bloque JAMAIS : hub saturé → événement abandonné, client
// lent → client évincé. L'état qui fait foi est en base, le flux n'est
// que de la télémétrie.
type Hub struct {
	logger     *slog.Logger
	register   chan *client
	unregister chan *client
	broadcast  chan domain.ProgressEvent
	clients    map[*client]struct{}
	done       chan struct{} // fermé à la sortie de Run
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

// Run : goroutine unique propriétaire de la map clients — pas de verrou,
// pas de blocage côté publieurs.
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
					// client lent : évincé, jamais attendu
					h.logger.Warn("dropping slow websocket client")
					delete(h.clients, c)
					close(c.send)
				}
			}
		}
	}
}

func (h *Hub) Publish(ev domain.ProgressEvent) {
	select {
	case h.broadcast <- ev:
	default: // hub saturé : on abandonne l'événement
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

type client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan domain.ProgressEvent
}

// writePump : seule goroutine à écrire sur la connexion ; pings de survie.
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

// readPump : draine les trames entrantes (pongs) et détecte la fermeture.
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
