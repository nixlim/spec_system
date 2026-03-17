package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func setupOTELReceiver(t *testing.T) (*OTELReceiver, *WebSocketHub) {
	t.Helper()
	hub := NewWebSocketHub()
	emitter := specworkflow.NewChannelEmitter(64)
	recv := NewOTELReceiver(hub, emitter)
	return recv, hub
}

// ---------------------------------------------------------------------------
// TestHandleMetrics
// ---------------------------------------------------------------------------

func TestHandleMetrics_EmptyBody(t *testing.T) {
	recv, _ := setupOTELReceiver(t)

	body := `{"resourceMetrics": []}`
	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	recv.HandleMetrics(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	// Should return empty JSON object.
	if len(resp) != 0 {
		t.Errorf("expected empty JSON response, got %v", resp)
	}
}

func TestHandleMetrics_MethodNotAllowed(t *testing.T) {
	recv, _ := setupOTELReceiver(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/metrics", nil)
	rec := httptest.NewRecorder()
	recv.HandleMetrics(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestHandleMetrics_InvalidJSON(t *testing.T) {
	recv, _ := setupOTELReceiver(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewBufferString("not json"))
	rec := httptest.NewRecorder()
	recv.HandleMetrics(rec, req)

	// Should still return 200 per OTLP protocol.
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for invalid JSON, got %d", rec.Code)
	}
}

func TestHandleMetrics_TokenUsage(t *testing.T) {
	recv, _ := setupOTELReceiver(t)

	asInt := int64(1000)
	strInput := "input"
	body := otlpMetricsRequest{
		ResourceMetrics: []otlpResourceMetrics{{
			ScopeMetrics: []otlpScopeMetrics{{
				Metrics: []otlpMetric{{
					Name: "claude_code.token.usage",
					Sum: &otlpSum{
						DataPoints: []otlpDataPoint{{
							AsInt: &asInt,
							Attributes: []otlpKeyValue{{
								Key:   "type",
								Value: otlpAnyValue{StringValue: &strInput},
							}},
						}},
					},
				}},
			}},
		}},
	}

	data, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	recv.HandleMetrics(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	recv.mu.Lock()
	defer recv.mu.Unlock()
	if recv.inputTokens != 1000 {
		t.Errorf("expected inputTokens=1000, got %d", recv.inputTokens)
	}
}

func TestHandleMetrics_CostUsage(t *testing.T) {
	recv, _ := setupOTELReceiver(t)

	cost := 0.05
	body := otlpMetricsRequest{
		ResourceMetrics: []otlpResourceMetrics{{
			ScopeMetrics: []otlpScopeMetrics{{
				Metrics: []otlpMetric{{
					Name: "claude_code.cost.usage",
					Sum: &otlpSum{
						DataPoints: []otlpDataPoint{{
							AsDouble: &cost,
						}},
					},
				}},
			}},
		}},
	}

	data, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(data))
	rec := httptest.NewRecorder()
	recv.HandleMetrics(rec, req)

	recv.mu.Lock()
	defer recv.mu.Unlock()
	if recv.totalCostUSD != 0.05 {
		t.Errorf("expected totalCostUSD=0.05, got %f", recv.totalCostUSD)
	}
}

// ---------------------------------------------------------------------------
// TestHandleLogs
// ---------------------------------------------------------------------------

func TestHandleLogs_EmptyBody(t *testing.T) {
	recv, _ := setupOTELReceiver(t)

	body := `{"resourceLogs": []}`
	req := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	recv.HandleLogs(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestHandleLogs_MethodNotAllowed(t *testing.T) {
	recv, _ := setupOTELReceiver(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/logs", nil)
	rec := httptest.NewRecorder()
	recv.HandleLogs(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestHandleLogs_ToolResult(t *testing.T) {
	recv, _ := setupOTELReceiver(t)

	eventName := "claude_code.tool_result"
	toolName := "Read"
	durMS := float64(150)
	successStr := "true"

	body := otlpLogsRequest{
		ResourceLogs: []otlpResourceLogs{{
			ScopeLogs: []otlpScopeLogs{{
				LogRecords: []otlpLogRecord{{
					Attributes: []otlpKeyValue{
						{Key: "event.name", Value: otlpAnyValue{StringValue: &eventName}},
						{Key: "tool_name", Value: otlpAnyValue{StringValue: &toolName}},
						{Key: "success", Value: otlpAnyValue{StringValue: &successStr}},
						{Key: "duration_ms", Value: otlpAnyValue{DoubleValue: &durMS}},
					},
				}},
			}},
		}},
	}

	data, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewReader(data))
	rec := httptest.NewRecorder()
	recv.HandleLogs(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	recv.mu.Lock()
	defer recv.mu.Unlock()
	if len(recv.toolResults) != 1 {
		t.Fatalf("expected 1 tool result, got %d", len(recv.toolResults))
	}
	if recv.toolResults[0].ToolName != "Read" {
		t.Errorf("expected tool_name 'Read', got %q", recv.toolResults[0].ToolName)
	}
	if !recv.toolResults[0].Success {
		t.Error("expected success=true")
	}
}

// ---------------------------------------------------------------------------
// TestResetMetrics
// ---------------------------------------------------------------------------

func TestResetMetrics(t *testing.T) {
	recv, _ := setupOTELReceiver(t)

	recv.mu.Lock()
	recv.inputTokens = 500
	recv.outputTokens = 200
	recv.totalCostUSD = 1.5
	recv.totalAPICalls = 10
	recv.toolResults = []ToolResultEvent{{ToolName: "test"}}
	recv.mu.Unlock()

	recv.ResetMetrics()

	recv.mu.Lock()
	defer recv.mu.Unlock()
	if recv.inputTokens != 0 || recv.outputTokens != 0 || recv.totalCostUSD != 0 || recv.totalAPICalls != 0 || recv.toolResults != nil {
		t.Error("ResetMetrics did not clear all fields")
	}
}

// ---------------------------------------------------------------------------
// TestHelpers
// ---------------------------------------------------------------------------

func TestGetAttributeString(t *testing.T) {
	s := "hello"
	attrs := []otlpKeyValue{
		{Key: "foo", Value: otlpAnyValue{StringValue: &s}},
	}
	if got := getAttributeString(attrs, "foo"); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
	if got := getAttributeString(attrs, "bar"); got != "" {
		t.Errorf("expected empty string for missing key, got %q", got)
	}
}

func TestGetAttributeFloat(t *testing.T) {
	d := 3.14
	attrs := []otlpKeyValue{
		{Key: "pi", Value: otlpAnyValue{DoubleValue: &d}},
	}
	if got := getAttributeFloat(attrs, "pi"); got != 3.14 {
		t.Errorf("expected 3.14, got %f", got)
	}
	if got := getAttributeFloat(attrs, "missing"); got != 0 {
		t.Errorf("expected 0 for missing key, got %f", got)
	}
}

func TestGetNumericValue(t *testing.T) {
	intVal := int64(42)
	doubleVal := 3.14

	if got := getNumericValue(otlpDataPoint{AsInt: &intVal}); got != 42 {
		t.Errorf("expected 42, got %f", got)
	}
	if got := getNumericValue(otlpDataPoint{AsDouble: &doubleVal}); got != 3.14 {
		t.Errorf("expected 3.14, got %f", got)
	}
	if got := getNumericValue(otlpDataPoint{}); got != 0 {
		t.Errorf("expected 0 for empty, got %f", got)
	}
}
