// Package api provides HTTP handlers for the adversarial spec system.
// This file implements an embedded OTLP gRPC receiver that accepts
// metrics and logs from child Claude Code processes (or the parent Claude
// Code process via global settings.json), extracts relevant telemetry
// (token usage, cost, tool results), and broadcasts them to the dashboard
// via WebSocket events.
package api

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/grpc"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// OTELReceiver accepts OTLP gRPC requests and converts them into
// dashboard-friendly WebSocket events.
type OTELReceiver struct {
	colmetricspb.UnimplementedMetricsServiceServer

	hub     *WebSocketHub
	emitter specworkflow.EventEmitter
	server  *grpc.Server
	mu      sync.Mutex

	// Accumulated metrics from OTLP metric exports.
	inputTokens       int64
	outputTokens      int64
	cacheReadTokens   int64
	cacheCreateTokens int64
	totalCostUSD      float64
	totalAPICalls     int
	toolResults       []ToolResultEvent
	broadcastCount    int
}

// otelLogsHandler implements LogsServiceServer separately from OTELReceiver.
// Both MetricsServiceServer and LogsServiceServer define an Export method
// with different signatures, so they cannot coexist on the same struct.
type otelLogsHandler struct {
	collogspb.UnimplementedLogsServiceServer
	recv *OTELReceiver
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
// gRPC server lifecycle
// ---------------------------------------------------------------------------

// Start binds a gRPC server to the given port and begins accepting OTLP
// metrics and logs exports. Returns an error if the port is already in use.
func (recv *OTELReceiver) Start(port int) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("otel listen on port %d: %w", port, err)
	}

	recv.server = grpc.NewServer()
	colmetricspb.RegisterMetricsServiceServer(recv.server, recv)
	collogspb.RegisterLogsServiceServer(recv.server, &otelLogsHandler{recv: recv})

	log.Printf("[otel] gRPC OTLP receiver listening on :%d", port)
	go func() {
		if err := recv.server.Serve(lis); err != nil {
			log.Printf("[otel] gRPC server error: %v", err)
		}
	}()
	return nil
}

// Stop gracefully shuts down the gRPC server.
func (recv *OTELReceiver) Stop() {
	if recv.server != nil {
		recv.server.GracefulStop()
	}
}

// ---------------------------------------------------------------------------
// MetricsServiceServer implementation
// ---------------------------------------------------------------------------

// Export handles incoming ExportMetricsServiceRequest RPCs.
func (recv *OTELReceiver) Export(_ context.Context, req *colmetricspb.ExportMetricsServiceRequest) (*colmetricspb.ExportMetricsServiceResponse, error) {
	if req == nil {
		return &colmetricspb.ExportMetricsServiceResponse{}, nil
	}

	changed := false
	for _, rm := range req.GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				if recv.processMetric(m) {
					changed = true
				}
			}
		}
	}

	if changed {
		recv.emitMetricsEvent()
	}

	return &colmetricspb.ExportMetricsServiceResponse{}, nil
}

// ---------------------------------------------------------------------------
// Metric processing
// ---------------------------------------------------------------------------

func (recv *OTELReceiver) processMetric(m *metricspb.Metric) bool {
	var dataPoints []*metricspb.NumberDataPoint
	switch d := m.GetData().(type) {
	case *metricspb.Metric_Sum:
		if d.Sum != nil {
			dataPoints = d.Sum.GetDataPoints()
		}
	case *metricspb.Metric_Gauge:
		if d.Gauge != nil {
			dataPoints = d.Gauge.GetDataPoints()
		}
	default:
		return false // skip histogram, summary, etc.
	}

	changed := false
	for _, dp := range dataPoints {
		var value float64
		switch v := dp.GetValue().(type) {
		case *metricspb.NumberDataPoint_AsDouble:
			value = v.AsDouble
		case *metricspb.NumberDataPoint_AsInt:
			value = float64(v.AsInt)
		}
		if value == 0 {
			continue
		}

		attrs := extractAttributes(dp.GetAttributes())

		recv.mu.Lock()
		switch m.GetName() {
		case "claude_code.token.usage", "claude_code.tokens":
			tokenType := attrs["type"]
			switch tokenType {
			case "input":
				recv.inputTokens += int64(value)
				changed = true
			case "output":
				recv.outputTokens += int64(value)
				changed = true
			case "cacheRead", "cache_read":
				recv.cacheReadTokens += int64(value)
				changed = true
			case "cacheCreation", "cache_creation":
				recv.cacheCreateTokens += int64(value)
				changed = true
			default:
				if tokenType == "" {
					recv.inputTokens += int64(value)
					changed = true
				}
			}
		case "claude_code.cost.usage", "claude_code.cost":
			recv.totalCostUSD += value
			changed = true
		case "claude_code.api.requests", "claude_code.api_calls", "claude_code.session.count":
			recv.totalAPICalls += int(value)
			changed = true
		}
		recv.mu.Unlock()
	}
	return changed
}

// ---------------------------------------------------------------------------
// LogsServiceServer implementation (via otelLogsHandler)
// ---------------------------------------------------------------------------

// Export handles incoming ExportLogsServiceRequest RPCs.
func (h *otelLogsHandler) Export(_ context.Context, req *collogspb.ExportLogsServiceRequest) (*collogspb.ExportLogsServiceResponse, error) {
	if req == nil {
		return &collogspb.ExportLogsServiceResponse{}, nil
	}

	for _, rl := range req.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			for _, lr := range sl.GetLogRecords() {
				h.recv.processLogRecord(lr)
			}
		}
	}

	return &collogspb.ExportLogsServiceResponse{}, nil
}

// ---------------------------------------------------------------------------
// Log processing
// ---------------------------------------------------------------------------

func (recv *OTELReceiver) processLogRecord(lr *logspb.LogRecord) {
	// Determine event name: prefer EventName field, fall back to body string.
	eventName := lr.GetEventName()
	if eventName == "" && lr.GetBody() != nil {
		if sv, ok := lr.GetBody().GetValue().(*commonpb.AnyValue_StringValue); ok {
			eventName = sv.StringValue
		}
	}

	attrs := extractLogAttributes(lr.GetAttributes())
	now := time.Now().UTC().Format(time.RFC3339)

	switch eventName {
	case "claude_code.tool_result", "tool_result":
		toolName := attrString(attrs, "tool_name")
		if toolName == "" {
			toolName = attrString(attrs, "tool.name")
		}
		success := attrString(attrs, "success") != "false"
		durationMS := attrFloat(attrs, "duration_ms")

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
		model := attrString(attrs, "model")
		costUSD := attrFloat(attrs, "cost_usd")
		durationMS := attrFloat(attrs, "duration_ms")

		// Accumulate from log events as well.
		recv.mu.Lock()
		recv.totalAPICalls++
		if costUSD > 0 {
			recv.totalCostUSD += costUSD
		}
		if inTok := attrFloat(attrs, "input_tokens"); inTok > 0 {
			recv.inputTokens += int64(inTok)
		}
		if outTok := attrFloat(attrs, "output_tokens"); outTok > 0 {
			recv.outputTokens += int64(outTok)
		}
		if cacheTok := attrFloat(attrs, "cache_read_tokens"); cacheTok > 0 {
			recv.cacheReadTokens += int64(cacheTok)
		}
		recv.mu.Unlock()

		recv.emitMetricsEvent()

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
		errType := attrString(attrs, "error_type")
		if errType == "" {
			errType = attrString(attrs, "error")
		}
		log.Printf("[otel] API error reported: %s", errType)
	}
}

// ---------------------------------------------------------------------------
// Event emission
// ---------------------------------------------------------------------------

func (recv *OTELReceiver) emitMetricsEvent() {
	recv.mu.Lock()
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
	recv.mu.Unlock()

	// Log every 10th update to avoid noise (metrics arrive every 5s).
	recv.broadcastCount++
	if recv.broadcastCount%10 == 1 {
		log.Printf("[otel] metrics: in=%d out=%d cache=%d cost=$%.4f api_calls=%d",
			payload.InputTokens, payload.OutputTokens, payload.CacheReadTokens,
			payload.TotalCostUSD, payload.TotalAPICalls)
	}

	event := specworkflow.EventEnvelope{
		Event: specworkflow.EventAgentMetrics,
		Data:  payload,
	}
	recv.hub.Broadcast(event)
}

// ---------------------------------------------------------------------------
// Helpers — protobuf attribute extraction
// ---------------------------------------------------------------------------

// extractAttributes converts a slice of OTLP KeyValue pairs to a string map.
func extractAttributes(kvs []*commonpb.KeyValue) map[string]string {
	attrs := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		attrs[kv.GetKey()] = anyValueToString(kv.GetValue())
	}
	return attrs
}

// extractLogAttributes converts OTLP KeyValue pairs to an interface{} map,
// preserving numeric types for accurate accumulation.
func extractLogAttributes(kvs []*commonpb.KeyValue) map[string]interface{} {
	attrs := make(map[string]interface{}, len(kvs))
	for _, kv := range kvs {
		attrs[kv.GetKey()] = anyValueToInterface(kv.GetValue())
	}
	return attrs
}

func anyValueToString(v *commonpb.AnyValue) string {
	if v == nil {
		return ""
	}
	switch val := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return val.StringValue
	case *commonpb.AnyValue_IntValue:
		return fmt.Sprintf("%d", val.IntValue)
	case *commonpb.AnyValue_DoubleValue:
		return fmt.Sprintf("%f", val.DoubleValue)
	case *commonpb.AnyValue_BoolValue:
		return fmt.Sprintf("%t", val.BoolValue)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func anyValueToInterface(v *commonpb.AnyValue) interface{} {
	if v == nil {
		return nil
	}
	switch val := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return val.StringValue
	case *commonpb.AnyValue_IntValue:
		return float64(val.IntValue)
	case *commonpb.AnyValue_DoubleValue:
		return val.DoubleValue
	case *commonpb.AnyValue_BoolValue:
		return val.BoolValue
	default:
		return fmt.Sprintf("%v", v)
	}
}

// attrString extracts a string value from an interface{} attribute map.
func attrString(attrs map[string]interface{}, key string) string {
	v, ok := attrs[key]
	if !ok || v == nil {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	default:
		return fmt.Sprintf("%v", s)
	}
}

// attrFloat extracts a float64 value from an interface{} attribute map.
func attrFloat(attrs map[string]interface{}, key string) float64 {
	v, ok := attrs[key]
	if !ok || v == nil {
		return 0
	}
	switch f := v.(type) {
	case float64:
		return f
	case string:
		// Try parsing string representation of numbers.
		var val float64
		fmt.Sscanf(f, "%f", &val)
		return val
	default:
		return 0
	}
}

// ---------------------------------------------------------------------------
// ResetMetrics
// ---------------------------------------------------------------------------

// ResetMetrics resets accumulated metrics. Useful when a new workflow starts.
func (recv *OTELReceiver) ResetMetrics() {
	recv.mu.Lock()
	defer recv.mu.Unlock()
	recv.inputTokens = 0
	recv.outputTokens = 0
	recv.cacheReadTokens = 0
	recv.cacheCreateTokens = 0
	recv.totalCostUSD = 0
	recv.totalAPICalls = 0
	recv.toolResults = nil
}
