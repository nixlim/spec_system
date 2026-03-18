// Package api provides HTTP handlers for the adversarial spec system.
// This file implements a minimal WebSocket hub for broadcasting workflow
// events to connected dashboard clients. It uses a raw HTTP Upgrade
// handshake (RFC 6455) with no external dependencies.
package api

import (
	"encoding/binary"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
	"golang.org/x/net/websocket"
)

// ---------------------------------------------------------------------------
// WebSocket connection wrapper
// ---------------------------------------------------------------------------

// wsConn wraps a raw net.Conn with WebSocket framing helpers.
type wsConn struct {
	conn net.Conn
	mu   sync.Mutex
	done chan struct{} // closed when the connection is shutting down
}

// writeMessage sends a text WebSocket frame to the connection.
func (c *wsConn) writeMessage(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Set a write deadline to avoid blocking indefinitely on slow clients.
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

	frame := buildTextFrame(data)
	_, err := c.conn.Write(frame)
	return err
}

// sendPing sends a JSON keepalive message to the client.
func (c *wsConn) sendPing() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	frame := buildTextFrame([]byte(`{"event":"ping"}`))
	_, err := c.conn.Write(frame)
	return err
}

// close sends a close frame and closes the underlying connection.
func (c *wsConn) close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Signal the ping goroutine to stop.
	select {
	case <-c.done:
	default:
		close(c.done)
	}

	// Send close frame (opcode 0x8).
	closeFrame := []byte{0x88, 0x00}
	_ = c.conn.SetWriteDeadline(time.Now().Add(1 * time.Second))
	_, _ = c.conn.Write(closeFrame)
	c.conn.Close()
}

// buildTextFrame constructs a WebSocket text frame (opcode 0x1, FIN bit set,
// no masking — server-to-client frames are unmasked per RFC 6455).
func buildTextFrame(payload []byte) []byte {
	length := len(payload)
	var frame []byte

	// First byte: FIN + opcode 0x1 (text).
	frame = append(frame, 0x81)

	// Payload length encoding.
	if length < 126 {
		frame = append(frame, byte(length))
	} else if length < 65536 {
		frame = append(frame, 126)
		lenBytes := make([]byte, 2)
		binary.BigEndian.PutUint16(lenBytes, uint16(length))
		frame = append(frame, lenBytes...)
	} else {
		frame = append(frame, 127)
		lenBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(lenBytes, uint64(length))
		frame = append(frame, lenBytes...)
	}

	frame = append(frame, payload...)
	return frame
}

// readFrames reads WebSocket frames from the connection. It handles ping
// frames by sending pong replies, and returns when a close frame is received
// or the connection errors. This keeps the connection alive and detects
// client disconnection.
func readFrames(conn *wsConn) {
	buf := make([]byte, 4096)
	for {
		_ = conn.conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		n, err := conn.conn.Read(buf)
		if err != nil {
			return
		}
		if n < 2 {
			continue
		}

		opcode := buf[0] & 0x0F

		switch opcode {
		case 0x8: // Close frame
			return
		case 0x9: // Ping — reply with pong
			pong := []byte{0x8A, 0x00} // FIN + pong opcode, zero length
			conn.mu.Lock()
			_ = conn.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_, _ = conn.conn.Write(pong)
			conn.mu.Unlock()
		default:
			// Ignore other frames (text/binary from client).
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
