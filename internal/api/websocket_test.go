package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
	"golang.org/x/net/websocket"
)

// ---------------------------------------------------------------------------
// TestBuildTextFrame
// ---------------------------------------------------------------------------

func TestBuildTextFrame_Small(t *testing.T) {
	payload := []byte("hello")
	frame := buildTextFrame(payload)

	// First byte: FIN + text opcode = 0x81
	if frame[0] != 0x81 {
		t.Errorf("expected first byte 0x81, got 0x%02x", frame[0])
	}
	// Second byte: payload length = 5 (no mask bit for server frames)
	if frame[1] != 5 {
		t.Errorf("expected length byte 5, got %d", frame[1])
	}
	// Payload follows immediately.
	if string(frame[2:]) != "hello" {
		t.Errorf("expected payload 'hello', got %q", string(frame[2:]))
	}
}

func TestBuildTextFrame_Medium(t *testing.T) {
	// 200 bytes — should use 126 + 2-byte length encoding.
	payload := []byte(strings.Repeat("x", 200))
	frame := buildTextFrame(payload)

	if frame[0] != 0x81 {
		t.Errorf("expected first byte 0x81, got 0x%02x", frame[0])
	}
	if frame[1] != 126 {
		t.Errorf("expected length marker 126, got %d", frame[1])
	}
	// 2-byte big-endian length at bytes 2-3.
	length := int(frame[2])<<8 | int(frame[3])
	if length != 200 {
		t.Errorf("expected extended length 200, got %d", length)
	}
	if len(frame) != 4+200 {
		t.Errorf("expected total frame length %d, got %d", 4+200, len(frame))
	}
}

func TestBuildTextFrame_Empty(t *testing.T) {
	frame := buildTextFrame([]byte{})
	if frame[0] != 0x81 {
		t.Errorf("expected first byte 0x81, got 0x%02x", frame[0])
	}
	if frame[1] != 0 {
		t.Errorf("expected length 0, got %d", frame[1])
	}
	if len(frame) != 2 {
		t.Errorf("expected frame length 2, got %d", len(frame))
	}
}

// headerContains was removed (now using golang.org/x/net/websocket).

// ---------------------------------------------------------------------------
// TestWebSocketHub
// ---------------------------------------------------------------------------

func TestWebSocketHub_NewHub(t *testing.T) {
	hub := NewWebSocketHub()
	if hub.ClientCount() != 0 {
		t.Errorf("expected 0 clients, got %d", hub.ClientCount())
	}
}

func TestWebSocketHub_Broadcast_NoClients(t *testing.T) {
	hub := NewWebSocketHub()
	// Broadcast with no clients should not panic.
	event := specworkflow.EventEnvelope{
		Event: "test_event",
		Data:  map[string]string{"key": "value"},
	}
	hub.Broadcast(event)
}

// ---------------------------------------------------------------------------
// TestHandleWebSocket_RejectsNonUpgrade
// ---------------------------------------------------------------------------

func TestHandleWebSocket_ConnectsAndRegisters(t *testing.T) {
	hub := NewWebSocketHub()

	mux := http.NewServeMux()
	mux.Handle("/ws", HandleWebSocket(hub))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Connect via golang.org/x/net/websocket client.
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	ws, err := websocket.Dial(wsURL, "", srv.URL)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	defer ws.Close()

	// Give hub time to register the client.
	time.Sleep(50 * time.Millisecond)
	if hub.ClientCount() != 1 {
		t.Errorf("expected 1 client, got %d", hub.ClientCount())
	}
}

// ---------------------------------------------------------------------------
// TestStartBroadcasting
// ---------------------------------------------------------------------------

func TestStartBroadcasting_ClosedChannel(t *testing.T) {
	hub := NewWebSocketHub()
	emitter := specworkflow.NewChannelEmitter(1)
	emitter.Close()

	// StartBroadcasting should return immediately when channel is closed.
	done := make(chan struct{})
	go func() {
		hub.StartBroadcasting(emitter)
		close(done)
	}()

	select {
	case <-done:
		// OK, returned as expected.
	case <-time.After(2 * time.Second):
		t.Fatal("StartBroadcasting did not return after channel close")
	}
}

// ---------------------------------------------------------------------------
// TestBroadcast_MarshalEvent
// ---------------------------------------------------------------------------

func TestBroadcast_MarshalEvent(t *testing.T) {
	// Verify that event marshalling works correctly.
	event := specworkflow.EventEnvelope{
		Event: "spec_version",
		Data: specworkflow.SpecVersionEvent{
			Version:   1,
			Round:     1,
			Timestamp: "2025-01-01T00:00:00Z",
			FilePath:  "/tmp/spec-v1.md",
		},
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to marshal event: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded["event"] != "spec_version" {
		t.Errorf("expected event 'spec_version', got %v", decoded["event"])
	}
}
