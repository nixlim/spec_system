// Package api provides HTTP handlers for the adversarial spec system.
// This file implements an embedded OTLP HTTP receiver that accepts
// metrics and logs from child Claude Code processes, extracts relevant
// telemetry (token usage, cost, tool results), and broadcasts them
// to the dashboard via WebSocket events.
package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// OTELReceiver accepts OTLP HTTP/JSON requests and converts them into
// dashboard-friendly WebSocket events.
type OTELReceiver struct {
	hub     *WebSocketHub
	emitter specworkflow.EventEmitter
	mu      sync.Mutex

	// Accumulated metrics from OTLP metric exports.
	inputTokens     int64
	outputTokens    int64
	cacheReadTokens int64
	totalCostUSD    float64
	totalAPICalls   int
	toolResults     []ToolResultEvent
}

// ToolResultEvent represents a single tool invocation reported via OTEL logs.
type ToolResultEvent struct {
	ToolName   string  `json:"tool_name"`
	Success    bool    `json:"success"`
	DurationMS float64 `json:"duration_ms"`
	Timestamp  string  `json:"timestamp"`
}

// AgentMetricsPayload is the WebSocket event payload for agent_metrics events.
type AgentMetricsPayload struct {
	TotalTokens     int64             `json:"total_tokens"`
	TotalCostUSD    float64           `json:"total_cost_usd"`
	TotalAPICalls   int               `json:"total_api_calls"`
	InputTokens     int64             `json:"input_tokens"`
	OutputTokens    int64             `json:"output_tokens"`
	CacheReadTokens int64             `json:"cache_read_tokens"`
	RecentTools     []ToolResultEvent `json:"recent_tools"`
	Timestamp       string            `json:"timestamp"`
}

// AgentToolPayload is the WebSocket event payload for agent_tool_event events.
type AgentToolPayload struct {
	ToolName   string  `json:"tool_name"`
	Success    bool    `json:"success"`
	DurationMS float64 `json:"duration_ms"`
	Timestamp  string  `json:"timestamp"`
}

// AgentAPIPayload is the WebSocket event payload for agent_api_event events.
type AgentAPIPayload struct {
	Model      string  `json:"model"`
	CostUSD    float64 `json:"cost_usd"`
	DurationMS float64 `json:"duration_ms"`
	Timestamp  string  `json:"timestamp"`
}

// NewOTELReceiver creates an OTELReceiver that broadcasts events to the
// given hub and emitter.
func NewOTELReceiver(hub *WebSocketHub, emitter specworkflow.EventEmitter) *OTELReceiver {
	return &OTELReceiver{
		hub:     hub,
		emitter: emitter,
	}
}

// ---------------------------------------------------------------------------
// OTLP JSON structures (simplified; we only parse what we need)
// ---------------------------------------------------------------------------

// otlpMetricsRequest is the top-level OTLP metrics JSON structure.
type otlpMetricsRequest struct {
	ResourceMetrics []otlpResourceMetrics `json:"resourceMetrics"`
}

type otlpResourceMetrics struct {
	ScopeMetrics []otlpScopeMetrics `json:"scopeMetrics"`
}

type otlpScopeMetrics struct {
	Metrics []otlpMetric `json:"metrics"`
}

type otlpMetric struct {
	Name  string     `json:"name"`
	Sum   *otlpSum   `json:"sum,omitempty"`
	Gauge *otlpGauge `json:"gauge,omitempty"`
}

type otlpSum struct {
	DataPoints []otlpDataPoint `json:"dataPoints"`
}

type otlpGauge struct {
	DataPoints []otlpDataPoint `json:"dataPoints"`
}

type otlpDataPoint struct {
	AsInt      *int64         `json:"asInt,omitempty"`
	AsDouble   *float64       `json:"asDouble,omitempty"`
	Attributes []otlpKeyValue `json:"attributes"`
}

type otlpKeyValue struct {
	Key   string       `json:"key"`
	Value otlpAnyValue `json:"value"`
}

type otlpAnyValue struct {
	StringValue *string  `json:"stringValue,omitempty"`
	IntValue    *int64   `json:"intValue,omitempty"`
	DoubleValue *float64 `json:"doubleValue,omitempty"`
}

// otlpLogsRequest is the top-level OTLP logs JSON structure.
type otlpLogsRequest struct {
	ResourceLogs []otlpResourceLogs `json:"resourceLogs"`
}

type otlpResourceLogs struct {
	ScopeLogs []otlpScopeLogs `json:"scopeLogs"`
}

type otlpScopeLogs struct {
	LogRecords []otlpLogRecord `json:"logRecords"`
}

type otlpLogRecord struct {
	TimeUnixNano string         `json:"timeUnixNano,omitempty"`
	SeverityText string         `json:"severityText,omitempty"`
	Body         *otlpAnyValue  `json:"body,omitempty"`
	Attributes   []otlpKeyValue `json:"attributes"`
}

// ---------------------------------------------------------------------------
// HTTP Handlers
// ---------------------------------------------------------------------------

// HandleMetrics processes OTLP metric export requests (POST /v1/metrics).
func (recv *OTELReceiver) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024)) // 10 MB limit
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var req otlpMetricsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		// Accept the request even if we can't parse it (OTLP protocol).
		log.Printf("otel: failed to parse metrics JSON: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
		return
	}

	recv.processMetrics(req)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("{}"))
}

// HandleLogs processes OTLP log export requests (POST /v1/logs).
func (recv *OTELReceiver) HandleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var req otlpLogsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		log.Printf("otel: failed to parse logs JSON: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
		return
	}

	recv.processLogs(req)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("{}"))
}

// ---------------------------------------------------------------------------
// Metric processing
// ---------------------------------------------------------------------------

func (recv *OTELReceiver) processMetrics(req otlpMetricsRequest) {
	recv.mu.Lock()
	defer recv.mu.Unlock()

	changed := false
	for _, rm := range req.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				switch m.Name {
				case "claude_code.token.usage", "claude_code.tokens":
					changed = recv.processTokenMetric(m) || changed
				case "claude_code.cost.usage", "claude_code.cost":
					changed = recv.processCostMetric(m) || changed
				case "claude_code.api.requests", "claude_code.api_calls":
					changed = recv.processAPICallMetric(m) || changed
				}
			}
		}
	}

	if changed {
		recv.emitMetricsEvent()
	}
}

func (recv *OTELReceiver) processTokenMetric(m otlpMetric) bool {
	points := getDataPoints(m)
	if len(points) == 0 {
		return false
	}

	changed := false
	for _, dp := range points {
		val := getNumericValue(dp)
		if val == 0 {
			continue
		}
		tokenType := getAttributeString(dp.Attributes, "type")
		switch tokenType {
		case "input":
			recv.inputTokens += int64(val)
			changed = true
		case "output":
			recv.outputTokens += int64(val)
			changed = true
		case "cacheRead", "cache_read":
			recv.cacheReadTokens += int64(val)
			changed = true
		default:
			// Unknown type or aggregate — treat as total if no type.
			if tokenType == "" {
				recv.inputTokens += int64(val)
				changed = true
			}
		}
	}
	return changed
}

func (recv *OTELReceiver) processCostMetric(m otlpMetric) bool {
	points := getDataPoints(m)
	if len(points) == 0 {
		return false
	}
	for _, dp := range points {
		val := getNumericValue(dp)
		if val > 0 {
			recv.totalCostUSD += val
			return true
		}
	}
	return false
}

func (recv *OTELReceiver) processAPICallMetric(m otlpMetric) bool {
	points := getDataPoints(m)
	if len(points) == 0 {
		return false
	}
	for _, dp := range points {
		val := getNumericValue(dp)
		if val > 0 {
			recv.totalAPICalls += int(val)
			return true
		}
	}
	return false
}

func (recv *OTELReceiver) emitMetricsEvent() {
	total := recv.inputTokens + recv.outputTokens + recv.cacheReadTokens

	// Keep only the last 10 tool results.
	recent := recv.toolResults
	if len(recent) > 10 {
		recent = recent[len(recent)-10:]
	}

	payload := AgentMetricsPayload{
		TotalTokens:     total,
		TotalCostUSD:    recv.totalCostUSD,
		TotalAPICalls:   recv.totalAPICalls,
		InputTokens:     recv.inputTokens,
		OutputTokens:    recv.outputTokens,
		CacheReadTokens: recv.cacheReadTokens,
		RecentTools:     recent,
		Timestamp:       time.Now().UTC().Format(time.RFC3339),
	}

	event := specworkflow.EventEnvelope{
		Event: specworkflow.EventAgentMetrics,
		Data:  payload,
	}
	recv.hub.Broadcast(event)
}

// ---------------------------------------------------------------------------
// Log processing
// ---------------------------------------------------------------------------

func (recv *OTELReceiver) processLogs(req otlpLogsRequest) {
	for _, rl := range req.ResourceLogs {
		for _, sl := range rl.ScopeLogs {
			for _, record := range sl.LogRecords {
				recv.processLogRecord(record)
			}
		}
	}
}

func (recv *OTELReceiver) processLogRecord(record otlpLogRecord) {
	eventName := getAttributeString(record.Attributes, "event.name")
	if eventName == "" {
		// Try the body as event name.
		if record.Body != nil && record.Body.StringValue != nil {
			eventName = *record.Body.StringValue
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)

	switch eventName {
	case "claude_code.tool_result", "tool_result":
		toolName := getAttributeString(record.Attributes, "tool_name")
		if toolName == "" {
			toolName = getAttributeString(record.Attributes, "tool.name")
		}
		success := getAttributeString(record.Attributes, "success") != "false"
		durationMS := getAttributeFloat(record.Attributes, "duration_ms")

		toolEvent := ToolResultEvent{
			ToolName:   toolName,
			Success:    success,
			DurationMS: durationMS,
			Timestamp:  now,
		}

		recv.mu.Lock()
		recv.toolResults = append(recv.toolResults, toolEvent)
		recv.mu.Unlock()

		// Broadcast individual tool event.
		recv.hub.Broadcast(specworkflow.EventEnvelope{
			Event: specworkflow.EventAgentToolEvent,
			Data: AgentToolPayload{
				ToolName:   toolName,
				Success:    success,
				DurationMS: durationMS,
				Timestamp:  now,
			},
		})

	case "claude_code.api_request", "api_request":
		model := getAttributeString(record.Attributes, "model")
		costUSD := getAttributeFloat(record.Attributes, "cost_usd")
		durationMS := getAttributeFloat(record.Attributes, "duration_ms")

		recv.hub.Broadcast(specworkflow.EventEnvelope{
			Event: specworkflow.EventAgentAPIEvent,
			Data: AgentAPIPayload{
				Model:      model,
				CostUSD:    costUSD,
				DurationMS: durationMS,
				Timestamp:  now,
			},
		})

	case "claude_code.api_error", "api_error":
		// Log but don't broadcast as a separate event — it'll show up in agent_error.
		errType := getAttributeString(record.Attributes, "error_type")
		log.Printf("otel: API error reported: %s", errType)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func getDataPoints(m otlpMetric) []otlpDataPoint {
	if m.Sum != nil {
		return m.Sum.DataPoints
	}
	if m.Gauge != nil {
		return m.Gauge.DataPoints
	}
	return nil
}

func getNumericValue(dp otlpDataPoint) float64 {
	if dp.AsDouble != nil {
		return *dp.AsDouble
	}
	if dp.AsInt != nil {
		return float64(*dp.AsInt)
	}
	return 0
}

func getAttributeString(attrs []otlpKeyValue, key string) string {
	for _, kv := range attrs {
		if kv.Key == key && kv.Value.StringValue != nil {
			return *kv.Value.StringValue
		}
	}
	return ""
}

func getAttributeFloat(attrs []otlpKeyValue, key string) float64 {
	for _, kv := range attrs {
		if kv.Key == key {
			if kv.Value.DoubleValue != nil {
				return *kv.Value.DoubleValue
			}
			if kv.Value.IntValue != nil {
				return float64(*kv.Value.IntValue)
			}
		}
	}
	return 0
}

// ResetMetrics resets accumulated metrics. Useful when a new workflow starts.
func (recv *OTELReceiver) ResetMetrics() {
	recv.mu.Lock()
	defer recv.mu.Unlock()
	recv.inputTokens = 0
	recv.outputTokens = 0
	recv.cacheReadTokens = 0
	recv.totalCostUSD = 0
	recv.totalAPICalls = 0
	recv.toolResults = nil
}
