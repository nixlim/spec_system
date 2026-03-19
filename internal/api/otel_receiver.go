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

// MetricsAccumulator holds per-workflow accumulated telemetry data.
type MetricsAccumulator struct {
	inputTokens       int64
	outputTokens      int64
	cacheReadTokens   int64
	cacheCreateTokens int64
	totalCostUSD      float64
	totalAPICalls     int
	toolResults       []ToolResultEvent
	broadcastCount    int
}

// OTELReceiver accepts OTLP gRPC requests and converts them into
// dashboard-friendly WebSocket events.
type OTELReceiver struct {
	colmetricspb.UnimplementedMetricsServiceServer

	hub     *WebSocketHub
	emitter specworkflow.EventEmitter
	server  *grpc.Server
	mu      sync.RWMutex

	// Per-workflow accumulated metrics, keyed by workflow.feature.
	accumulators map[string]*MetricsAccumulator

	// metricsStore persists telemetry to SQLite so data survives
	// browser refreshes and server restarts. Nil if not configured.
	metricsStore *MetricsStore
	// featureNameFn returns the current workflow's feature name.
	// Used to associate metrics with the correct workflow run.
	// Nil if not configured (metrics won't be persisted).
	featureNameFn func() string
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
	FeatureName     string            `json:"feature_name"`
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
		hub:          hub,
		emitter:      emitter,
		accumulators: make(map[string]*MetricsAccumulator),
	}
}

// SetMetricsStore configures SQLite persistence for telemetry data.
// Must be called before Start. The featureNameFn callback returns the
// current workflow's feature name so metrics can be associated with the
// correct workflow run.
func (recv *OTELReceiver) SetMetricsStore(store *MetricsStore, featureNameFn func() string) {
	recv.metricsStore = store
	recv.featureNameFn = featureNameFn
}

// RestoreFromStore loads persisted aggregate metrics from SQLite into
// the in-memory accumulators so the OTEL receiver continues from where
// it left off after a server restart. Call after SetMetricsStore and
// before Start.
func (recv *OTELReceiver) RestoreFromStore(featureName string) {
	if recv.metricsStore == nil || featureName == "" {
		return
	}
	m, err := recv.metricsStore.GetWorkflowMetrics(featureName)
	if err != nil || m == nil {
		return
	}
	recv.mu.Lock()
	defer recv.mu.Unlock()
	acc := recv.getOrCreateAccumulatorLocked(featureName)
	acc.inputTokens = m.InputTokens
	acc.outputTokens = m.OutputTokens
	acc.cacheReadTokens = m.CacheReadTokens
	acc.totalCostUSD = m.TotalCostUSD
	acc.totalAPICalls = m.TotalAPICalls
	log.Printf("[otel] restored metrics from store: feature=%s cost=$%.4f api_calls=%d",
		featureName, m.TotalCostUSD, m.TotalAPICalls)
}

// getOrCreateAccumulatorLocked returns the accumulator for the given feature,
// creating one if it doesn't exist. Caller must hold recv.mu.
func (recv *OTELReceiver) getOrCreateAccumulatorLocked(featureName string) *MetricsAccumulator {
	acc, ok := recv.accumulators[featureName]
	if !ok {
		acc = &MetricsAccumulator{}
		recv.accumulators[featureName] = acc
	}
	return acc
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

// otelServiceName is the expected service.name resource attribute on
// incoming OTLP data. Data from other Claude Code instances is silently
// dropped so metrics only reflect our child processes.
const otelServiceName = "adversarial-spec-system"

// isOwnResource checks whether the OTLP resource carries our service name.
// Returns true if the resource matches or has no service.name (backward compat).
func isOwnResource(attrs []*commonpb.KeyValue) bool {
	for _, kv := range attrs {
		if kv.GetKey() == "service.name" {
			if sv, ok := kv.GetValue().GetValue().(*commonpb.AnyValue_StringValue); ok {
				return sv.StringValue == otelServiceName
			}
		}
	}
	// No service.name attribute — accept for backward compatibility.
	return true
}

// extractWorkflowFeature reads the workflow.feature resource attribute
// from the given OTLP KeyValue slice. Returns empty string if not found.
func extractWorkflowFeature(attrs []*commonpb.KeyValue) string {
	for _, kv := range attrs {
		if kv.GetKey() == "workflow.feature" {
			if sv, ok := kv.GetValue().GetValue().(*commonpb.AnyValue_StringValue); ok {
				return sv.StringValue
			}
		}
	}
	return ""
}

// Export handles incoming ExportMetricsServiceRequest RPCs.
func (recv *OTELReceiver) Export(_ context.Context, req *colmetricspb.ExportMetricsServiceRequest) (*colmetricspb.ExportMetricsServiceResponse, error) {
	if req == nil {
		return &colmetricspb.ExportMetricsServiceResponse{}, nil
	}

	// Track which features changed so we emit events per-feature.
	changedFeatures := make(map[string]bool)

	for _, rm := range req.GetResourceMetrics() {
		resAttrs := rm.GetResource().GetAttributes()
		// Filter: only process metrics from our own child processes.
		if !isOwnResource(resAttrs) {
			continue
		}
		// Extract workflow.feature; drop telemetry without it.
		featureName := extractWorkflowFeature(resAttrs)
		if featureName == "" {
			continue
		}

		recv.mu.Lock()
		acc := recv.getOrCreateAccumulatorLocked(featureName)
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				if processMetric(acc, m) {
					changedFeatures[featureName] = true
				}
			}
		}
		recv.mu.Unlock()
	}

	for feature := range changedFeatures {
		recv.emitMetricsEvent(feature)
	}

	return &colmetricspb.ExportMetricsServiceResponse{}, nil
}

// ---------------------------------------------------------------------------
// Metric processing
// ---------------------------------------------------------------------------

// processMetric accumulates a single OTLP metric into the given accumulator.
// Returns true if any values were accumulated.
func processMetric(acc *MetricsAccumulator, m *metricspb.Metric) bool {
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

		switch m.GetName() {
		case "claude_code.token.usage", "claude_code.tokens":
			tokenType := attrs["type"]
			switch tokenType {
			case "input":
				acc.inputTokens += int64(value)
				changed = true
			case "output":
				acc.outputTokens += int64(value)
				changed = true
			case "cacheRead", "cache_read":
				acc.cacheReadTokens += int64(value)
				changed = true
			case "cacheCreation", "cache_creation":
				acc.cacheCreateTokens += int64(value)
				changed = true
			default:
				if tokenType == "" {
					acc.inputTokens += int64(value)
					changed = true
				}
			}
		case "claude_code.cost.usage", "claude_code.cost":
			acc.totalCostUSD += value
			changed = true
		case "claude_code.api.requests", "claude_code.api_calls", "claude_code.session.count":
			acc.totalAPICalls += int(value)
			changed = true
		}
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
		resAttrs := rl.GetResource().GetAttributes()
		// Filter: only process logs from our own child processes.
		if !isOwnResource(resAttrs) {
			continue
		}
		// Extract workflow.feature; drop telemetry without it.
		featureName := extractWorkflowFeature(resAttrs)
		if featureName == "" {
			continue
		}
		for _, sl := range rl.GetScopeLogs() {
			for _, lr := range sl.GetLogRecords() {
				h.recv.processLogRecord(featureName, lr)
			}
		}
	}

	return &collogspb.ExportLogsServiceResponse{}, nil
}

// ---------------------------------------------------------------------------
// Log processing
// ---------------------------------------------------------------------------

func (recv *OTELReceiver) processLogRecord(featureName string, lr *logspb.LogRecord) {
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
		acc := recv.getOrCreateAccumulatorLocked(featureName)
		acc.toolResults = append(acc.toolResults, toolEvent)
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

		// Persist tool event to SQLite.
		recv.persistEvent(featureName, MetricEvent{
			EventType:  "tool",
			ToolName:   toolName,
			Success:    success,
			DurationMS: durationMS,
			Timestamp:  now,
		})

	case "claude_code.api_request", "api_request":
		model := attrString(attrs, "model")
		costUSD := attrFloat(attrs, "cost_usd")
		durationMS := attrFloat(attrs, "duration_ms")

		// Accumulate from log events as well.
		recv.mu.Lock()
		acc := recv.getOrCreateAccumulatorLocked(featureName)
		acc.totalAPICalls++
		if costUSD > 0 {
			acc.totalCostUSD += costUSD
		}
		if inTok := attrFloat(attrs, "input_tokens"); inTok > 0 {
			acc.inputTokens += int64(inTok)
		}
		if outTok := attrFloat(attrs, "output_tokens"); outTok > 0 {
			acc.outputTokens += int64(outTok)
		}
		if cacheTok := attrFloat(attrs, "cache_read_tokens"); cacheTok > 0 {
			acc.cacheReadTokens += int64(cacheTok)
		}
		recv.mu.Unlock()

		recv.emitMetricsEvent(featureName)

		recv.hub.Broadcast(specworkflow.EventEnvelope{
			Event: specworkflow.EventAgentAPIEvent,
			Data: AgentAPIPayload{
				Model:      model,
				CostUSD:    costUSD,
				DurationMS: durationMS,
				Timestamp:  now,
			},
		})

		// Persist API event to SQLite.
		recv.persistEvent(featureName, MetricEvent{
			EventType:  "api",
			Model:      model,
			Success:    true,
			DurationMS: durationMS,
			CostUSD:    costUSD,
			Timestamp:  now,
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

func (recv *OTELReceiver) emitMetricsEvent(featureName string) {
	recv.mu.Lock()
	acc := recv.getOrCreateAccumulatorLocked(featureName)
	total := acc.inputTokens + acc.outputTokens + acc.cacheReadTokens

	// Keep only the last 10 tool results.
	recent := acc.toolResults
	if len(recent) > 10 {
		recent = recent[len(recent)-10:]
	}

	payload := AgentMetricsPayload{
		FeatureName:     featureName,
		TotalTokens:     total,
		TotalCostUSD:    acc.totalCostUSD,
		TotalAPICalls:   acc.totalAPICalls,
		InputTokens:     acc.inputTokens,
		OutputTokens:    acc.outputTokens,
		CacheReadTokens: acc.cacheReadTokens,
		RecentTools:     recent,
		Timestamp:       time.Now().UTC().Format(time.RFC3339),
	}
	acc.broadcastCount++
	bc := acc.broadcastCount
	recv.mu.Unlock()

	// Log every 10th update to avoid noise (metrics arrive every 5s).
	if bc%10 == 1 {
		log.Printf("[otel] metrics [%s]: in=%d out=%d cache=%d cost=$%.4f api_calls=%d",
			featureName, payload.InputTokens, payload.OutputTokens, payload.CacheReadTokens,
			payload.TotalCostUSD, payload.TotalAPICalls)
	}

	event := specworkflow.EventEnvelope{
		Event: specworkflow.EventAgentMetrics,
		Data:  payload,
	}
	recv.hub.Broadcast(event)

	// Persist aggregate metrics to SQLite.
	recv.persistAggregateMetrics(featureName, payload)
}

// persistAggregateMetrics writes the current aggregate counters to SQLite.
// Runs asynchronously to avoid blocking the gRPC handler.
func (recv *OTELReceiver) persistAggregateMetrics(featureName string, p AgentMetricsPayload) {
	if recv.metricsStore == nil {
		return
	}
	if featureName == "" {
		return
	}
	go func() {
		if err := recv.metricsStore.UpsertWorkflowMetrics(WorkflowMetrics{
			FeatureName:     featureName,
			InputTokens:     p.InputTokens,
			OutputTokens:    p.OutputTokens,
			CacheReadTokens: p.CacheReadTokens,
			TotalCostUSD:    p.TotalCostUSD,
			TotalAPICalls:   p.TotalAPICalls,
			UpdatedAt:       p.Timestamp,
		}); err != nil {
			log.Printf("[otel] persist aggregate metrics: %v", err)
		}
	}()
}

// persistEvent writes a single tool or API event to SQLite.
// Runs asynchronously to avoid blocking the gRPC handler.
func (recv *OTELReceiver) persistEvent(featureName string, e MetricEvent) {
	if recv.metricsStore == nil {
		return
	}
	if featureName == "" {
		return
	}
	e.FeatureName = featureName
	go func() {
		if err := recv.metricsStore.RecordEvent(e); err != nil {
			log.Printf("[otel] persist event: %v", err)
		}
	}()
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
// GetCostUSD / ResetMetrics
// ---------------------------------------------------------------------------

// GetCostUSD returns the cumulative cost in USD tracked by the receiver.
// If featureName is provided, returns cost for that specific workflow.
// If no featureName is provided, returns the sum across all workflows.
// It implements the specworkflow.CostProvider interface (zero-arg form)
// so the orchestrator can sync authoritative OTEL cost data into workflow state.
func (recv *OTELReceiver) GetCostUSD(featureName ...string) float64 {
	recv.mu.RLock()
	defer recv.mu.RUnlock()
	if len(featureName) > 0 && featureName[0] != "" {
		if acc, ok := recv.accumulators[featureName[0]]; ok {
			return acc.totalCostUSD
		}
		return 0
	}
	// Sum across all workflows.
	var total float64
	for _, acc := range recv.accumulators {
		total += acc.totalCostUSD
	}
	return total
}

// ResetMetrics resets accumulated metrics. If featureName is provided,
// resets only that workflow's accumulators. If no featureName is provided,
// resets all workflows.
func (recv *OTELReceiver) ResetMetrics(featureName ...string) {
	recv.mu.Lock()
	defer recv.mu.Unlock()
	if len(featureName) > 0 && featureName[0] != "" {
		delete(recv.accumulators, featureName[0])
		return
	}
	// Reset all.
	recv.accumulators = make(map[string]*MetricsAccumulator)
}
