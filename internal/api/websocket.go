// Package api provides HTTP handlers for the adversarial spec system.
// This file implements a minimal WebSocket hub for broadcasting workflow
// events to connected dashboard clients. It uses a raw HTTP Upgrade
// handshake (RFC 6455) with no external dependencies.
package api

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
	"golang.org/x/net/websocket"
)

// ---------------------------------------------------------------------------
// WebSocket connection wrapper
// ---------------------------------------------------------------------------

// wsConn wraps a websocket.Conn with thread-safe writing and keepalive.
type wsConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
	done chan struct{} // closed when the connection is shutting down
}

// writeMessage sends a text WebSocket frame to the connection.
func (c *wsConn) writeMessage(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	// Use websocket.Message.Send to let the library handle framing.
	return websocket.Message.Send(c.conn, string(data))
}

// sendPing sends a JSON keepalive message to the client.
func (c *wsConn) sendPing() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return websocket.Message.Send(c.conn, `{"event":"ping"}`)
}

// close signals the ping goroutine to stop and closes the connection.
func (c *wsConn) close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	select {
	case <-c.done:
	default:
		close(c.done)
	}

	c.conn.Close()
}

// readFrames reads messages from the WebSocket connection until it closes.
// This keeps the connection alive and detects client disconnection.
// With golang.org/x/net/websocket, ping/pong is handled by the library.
func readFrames(conn *wsConn) {
	var msg string
	for {
		// websocket.Message.Receive blocks until a message arrives or the
		// connection closes. We don't use the messages — we just need to
		// detect disconnection.
		if err := websocket.Message.Receive(conn.conn, &msg); err != nil {
			return
		}
	}
}

// ---------------------------------------------------------------------------
// WebSocketHub
// ---------------------------------------------------------------------------

// WebSocketHub manages connected WebSocket clients and broadcasts events
// to all of them. It is safe for concurrent use.
type WebSocketHub struct {
	clients map[*wsConn]bool
	mu      sync.RWMutex
}

// NewWebSocketHub creates a new empty WebSocket hub.
func NewWebSocketHub() *WebSocketHub {
	return &WebSocketHub{
		clients: make(map[*wsConn]bool),
	}
}

// addClient registers a new WebSocket connection with the hub.
func (h *WebSocketHub) addClient(c *wsConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = true
}

// removeClient removes a WebSocket connection from the hub.
func (h *WebSocketHub) removeClient(c *wsConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
}

// ClientCount returns the number of currently connected clients.
func (h *WebSocketHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Broadcast sends a JSON-encoded event to all connected clients. Clients
// that fail to receive the message are removed from the hub.
func (h *WebSocketHub) Broadcast(event specworkflow.EventEnvelope) {
	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("websocket: failed to marshal event: %v", err)
		return
	}

	h.mu.RLock()
	clients := make([]*wsConn, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.RUnlock()

	var failed []*wsConn
	for _, c := range clients {
		if err := c.writeMessage(data); err != nil {
			failed = append(failed, c)
		}
	}

	// Remove failed clients.
	if len(failed) > 0 {
		h.mu.Lock()
		for _, c := range failed {
			delete(h.clients, c)
			go c.close()
		}
		h.mu.Unlock()
	}
}

// StartBroadcasting reads events from the ChannelEmitter and broadcasts
// them to all connected WebSocket clients. It runs until the emitter's
// channel is closed. Call this in a goroutine.
func (h *WebSocketHub) StartBroadcasting(emitter *specworkflow.ChannelEmitter) {
	for event := range emitter.Events() {
		h.Broadcast(event)
	}
}

// HandleWebSocket returns an http.Handler that upgrades connections to
// WebSocket using golang.org/x/net/websocket and registers them with the hub.
func HandleWebSocket(hub *WebSocketHub) http.Handler {
	return websocket.Handler(func(ws *websocket.Conn) {
		// Wrap in our wsConn type and register.
		wsc := &wsConn{conn: ws, done: make(chan struct{})}
		hub.addClient(wsc)

		// Send periodic keepalive pings so the connection survives long
		// agent runs (which can exceed browser/proxy idle timeouts).
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if err := wsc.sendPing(); err != nil {
						return
					}
				case <-wsc.done:
					return
				}
			}
		}()

		// Block until the client disconnects (read frames to detect close).
		readFrames(wsc)

		hub.removeClient(wsc)
		wsc.close()
		// websocket.Handler closes the connection after the handler returns.
	})
}
