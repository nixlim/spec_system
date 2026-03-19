package api

import (
	"context"
	"net"
	"testing"
	"time"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

const testFeatureName = "test-feature"

func setupOTELReceiver(t *testing.T) (*OTELReceiver, *WebSocketHub) {
	t.Helper()
	hub := NewWebSocketHub()
	emitter := specworkflow.NewChannelEmitter(64)
	recv := NewOTELReceiver(hub, emitter)
	return recv, hub
}

// grpcTestClients holds both metric and log gRPC clients for testing.
type grpcTestClients struct {
	metrics colmetricspb.MetricsServiceClient
	logs    collogspb.LogsServiceClient
}

// startTestGRPC creates a gRPC receiver on an ephemeral port and returns
// the receiver, connected clients, and the client connection for cleanup.
func startTestGRPC(t *testing.T) (*OTELReceiver, grpcTestClients, *grpc.ClientConn) {
	t.Helper()

	hub := NewWebSocketHub()
	emitter := specworkflow.NewChannelEmitter(64)
	recv := NewOTELReceiver(hub, emitter)

	// Manually bind to an ephemeral port.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	recv.server = grpc.NewServer()
	colmetricspb.RegisterMetricsServiceServer(recv.server, recv)
	collogspb.RegisterLogsServiceServer(recv.server, &otelLogsHandler{recv: recv})

	go func() {
		_ = recv.server.Serve(lis)
	}()

	// Connect a client.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		recv.Stop()
		t.Fatalf("failed to connect gRPC client: %v", err)
	}

	clients := grpcTestClients{
		metrics: colmetricspb.NewMetricsServiceClient(conn),
		logs:    collogspb.NewLogsServiceClient(conn),
	}
	return recv, clients, conn
}

// testResource returns an OTLP Resource with our service name and the given
// workflow.feature value. If featureName is empty, no workflow.feature is set.
func testResource(featureName string) *resourcepb.Resource {
	attrs := []*commonpb.KeyValue{
		{
			Key:   "service.name",
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: otelServiceName}},
		},
	}
	if featureName != "" {
		attrs = append(attrs, &commonpb.KeyValue{
			Key:   "workflow.feature",
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: featureName}},
		})
	}
	return &resourcepb.Resource{Attributes: attrs}
}

// getAcc is a test helper to safely read an accumulator for a feature.
func getAcc(t *testing.T, recv *OTELReceiver, featureName string) *MetricsAccumulator {
	t.Helper()
	recv.mu.RLock()
	defer recv.mu.RUnlock()
	acc, ok := recv.accumulators[featureName]
	if !ok {
		return nil
	}
	return acc
}

// ---------------------------------------------------------------------------
// TestExportMetrics via gRPC
// ---------------------------------------------------------------------------

func TestGRPCMetrics_EmptyRequest(t *testing.T) {
	recv, clients, conn := startTestGRPC(t)
	defer func() {
		conn.Close()
		recv.Stop()
	}()

	resp, err := clients.metrics.Export(context.Background(), &colmetricspb.ExportMetricsServiceRequest{})
	if err != nil {
		t.Fatalf("empty request should succeed: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestGRPCMetrics_TokenUsage(t *testing.T) {
	recv, clients, conn := startTestGRPC(t)
	defer func() {
		conn.Close()
		recv.Stop()
	}()

	ts := uint64(time.Now().UnixNano())
	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				Resource: testResource(testFeatureName),
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{
								Name: "claude_code.token.usage",
								Data: &metricspb.Metric_Sum{
									Sum: &metricspb.Sum{
										DataPoints: []*metricspb.NumberDataPoint{
											{
												TimeUnixNano: ts,
												Value:        &metricspb.NumberDataPoint_AsInt{AsInt: 1000},
												Attributes: []*commonpb.KeyValue{
													{
														Key:   "type",
														Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "input"}},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	resp, err := clients.metrics.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	acc := getAcc(t, recv, testFeatureName)
	if acc == nil {
		t.Fatal("expected accumulator for test feature")
	}
	if acc.inputTokens != 1000 {
		t.Errorf("expected inputTokens=1000, got %d", acc.inputTokens)
	}
}

func TestGRPCMetrics_CostUsage(t *testing.T) {
	recv, clients, conn := startTestGRPC(t)
	defer func() {
		conn.Close()
		recv.Stop()
	}()

	ts := uint64(time.Now().UnixNano())
	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				Resource: testResource(testFeatureName),
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{
								Name: "claude_code.cost.usage",
								Data: &metricspb.Metric_Sum{
									Sum: &metricspb.Sum{
										DataPoints: []*metricspb.NumberDataPoint{
											{
												TimeUnixNano: ts,
												Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: 0.05},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := clients.metrics.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	acc := getAcc(t, recv, testFeatureName)
	if acc == nil {
		t.Fatal("expected accumulator for test feature")
	}
	if acc.totalCostUSD != 0.05 {
		t.Errorf("expected totalCostUSD=0.05, got %f", acc.totalCostUSD)
	}
}

func TestGRPCMetrics_MultipleTokenTypes(t *testing.T) {
	recv, clients, conn := startTestGRPC(t)
	defer func() {
		conn.Close()
		recv.Stop()
	}()

	ts := uint64(time.Now().UnixNano())
	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				Resource: testResource(testFeatureName),
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{
								Name: "claude_code.token.usage",
								Data: &metricspb.Metric_Sum{
									Sum: &metricspb.Sum{
										DataPoints: []*metricspb.NumberDataPoint{
											{
												TimeUnixNano: ts,
												Value:        &metricspb.NumberDataPoint_AsInt{AsInt: 500},
												Attributes: []*commonpb.KeyValue{
													{Key: "type", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "input"}}},
												},
											},
											{
												TimeUnixNano: ts,
												Value:        &metricspb.NumberDataPoint_AsInt{AsInt: 200},
												Attributes: []*commonpb.KeyValue{
													{Key: "type", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "output"}}},
												},
											},
											{
												TimeUnixNano: ts,
												Value:        &metricspb.NumberDataPoint_AsInt{AsInt: 300},
												Attributes: []*commonpb.KeyValue{
													{Key: "type", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "cacheRead"}}},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := clients.metrics.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	acc := getAcc(t, recv, testFeatureName)
	if acc == nil {
		t.Fatal("expected accumulator for test feature")
	}
	if acc.inputTokens != 500 {
		t.Errorf("expected inputTokens=500, got %d", acc.inputTokens)
	}
	if acc.outputTokens != 200 {
		t.Errorf("expected outputTokens=200, got %d", acc.outputTokens)
	}
	if acc.cacheReadTokens != 300 {
		t.Errorf("expected cacheReadTokens=300, got %d", acc.cacheReadTokens)
	}
}

func TestGRPCMetrics_ServerSurvivesAfterEmptyRequest(t *testing.T) {
	recv, clients, conn := startTestGRPC(t)
	defer func() {
		conn.Close()
		recv.Stop()
	}()

	ctx := context.Background()

	// Empty request first.
	_, err := clients.metrics.Export(ctx, &colmetricspb.ExportMetricsServiceRequest{})
	if err != nil {
		t.Fatalf("empty request should succeed: %v", err)
	}

	// Then a real metric.
	ts := uint64(time.Now().UnixNano())
	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				Resource: testResource(testFeatureName),
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{
								Name: "claude_code.cost.usage",
								Data: &metricspb.Metric_Sum{
									Sum: &metricspb.Sum{
										DataPoints: []*metricspb.NumberDataPoint{
											{
												TimeUnixNano: ts,
												Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: 0.10},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	_, err = clients.metrics.Export(ctx, req)
	if err != nil {
		t.Fatalf("Export after empty request failed: %v", err)
	}

	acc := getAcc(t, recv, testFeatureName)
	if acc == nil {
		t.Fatal("expected accumulator for test feature")
	}
	if acc.totalCostUSD != 0.10 {
		t.Errorf("expected totalCostUSD=0.10, got %f", acc.totalCostUSD)
	}
}

// ---------------------------------------------------------------------------
// TestExportLogs via gRPC
// ---------------------------------------------------------------------------

func TestGRPCLogs_EmptyRequest(t *testing.T) {
	recv, clients, conn := startTestGRPC(t)
	defer func() {
		conn.Close()
		recv.Stop()
	}()

	resp, err := clients.logs.Export(context.Background(), &collogspb.ExportLogsServiceRequest{})
	if err != nil {
		t.Fatalf("empty request should succeed: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestGRPCLogs_ToolResult(t *testing.T) {
	recv, clients, conn := startTestGRPC(t)
	defer func() {
		conn.Close()
		recv.Stop()
	}()

	ts := uint64(time.Now().UnixNano())
	req := &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{
			{
				Resource: testResource(testFeatureName),
				ScopeLogs: []*logspb.ScopeLogs{
					{
						LogRecords: []*logspb.LogRecord{
							{
								TimeUnixNano: ts,
								EventName:    "claude_code.tool_result",
								Attributes: []*commonpb.KeyValue{
									{Key: "tool_name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "Read"}}},
									{Key: "success", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "true"}}},
									{Key: "duration_ms", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: 150}}},
								},
							},
						},
					},
				},
			},
		},
	}

	resp, err := clients.logs.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("logs Export failed: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	acc := getAcc(t, recv, testFeatureName)
	if acc == nil {
		t.Fatal("expected accumulator for test feature")
	}
	if len(acc.toolResults) != 1 {
		t.Fatalf("expected 1 tool result, got %d", len(acc.toolResults))
	}
	if acc.toolResults[0].ToolName != "Read" {
		t.Errorf("expected tool_name 'Read', got %q", acc.toolResults[0].ToolName)
	}
	if !acc.toolResults[0].Success {
		t.Error("expected success=true")
	}
}

func TestGRPCLogs_APIRequest(t *testing.T) {
	recv, clients, conn := startTestGRPC(t)
	defer func() {
		conn.Close()
		recv.Stop()
	}()

	ts := uint64(time.Now().UnixNano())
	req := &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{
			{
				Resource: testResource(testFeatureName),
				ScopeLogs: []*logspb.ScopeLogs{
					{
						LogRecords: []*logspb.LogRecord{
							{
								TimeUnixNano: ts,
								EventName:    "claude_code.api_request",
								Attributes: []*commonpb.KeyValue{
									{Key: "model", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "claude-sonnet-4-5-20250929"}}},
									{Key: "cost_usd", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: 0.07}}},
									{Key: "input_tokens", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 2000}}},
									{Key: "output_tokens", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 500}}},
									{Key: "duration_ms", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 3200}}},
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := clients.logs.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("logs Export failed: %v", err)
	}

	acc := getAcc(t, recv, testFeatureName)
	if acc == nil {
		t.Fatal("expected accumulator for test feature")
	}
	if acc.totalAPICalls != 1 {
		t.Errorf("expected totalAPICalls=1, got %d", acc.totalAPICalls)
	}
	if acc.totalCostUSD != 0.07 {
		t.Errorf("expected totalCostUSD=0.07, got %f", acc.totalCostUSD)
	}
	if acc.inputTokens != 2000 {
		t.Errorf("expected inputTokens=2000, got %d", acc.inputTokens)
	}
	if acc.outputTokens != 500 {
		t.Errorf("expected outputTokens=500, got %d", acc.outputTokens)
	}
}

func TestGRPCLogs_EventNameFallbackToBody(t *testing.T) {
	recv, clients, conn := startTestGRPC(t)
	defer func() {
		conn.Close()
		recv.Stop()
	}()

	ts := uint64(time.Now().UnixNano())
	req := &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{
			{
				Resource: testResource(testFeatureName),
				ScopeLogs: []*logspb.ScopeLogs{
					{
						LogRecords: []*logspb.LogRecord{
							{
								TimeUnixNano: ts,
								// EventName not set — use Body as fallback.
								Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "claude_code.tool_result"}},
								Attributes: []*commonpb.KeyValue{
									{Key: "tool_name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "Write"}}},
									{Key: "success", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "true"}}},
									{Key: "duration_ms", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: 200}}},
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := clients.logs.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("logs Export failed: %v", err)
	}

	acc := getAcc(t, recv, testFeatureName)
	if acc == nil {
		t.Fatal("expected accumulator for test feature")
	}
	if len(acc.toolResults) != 1 {
		t.Fatalf("expected 1 tool result, got %d", len(acc.toolResults))
	}
	if acc.toolResults[0].ToolName != "Write" {
		t.Errorf("expected tool_name 'Write', got %q", acc.toolResults[0].ToolName)
	}
}

// ---------------------------------------------------------------------------
// TestResetMetrics
// ---------------------------------------------------------------------------

func TestResetMetrics(t *testing.T) {
	recv, _ := setupOTELReceiver(t)

	recv.mu.Lock()
	acc := recv.getOrCreateAccumulatorLocked(testFeatureName)
	acc.inputTokens = 500
	acc.outputTokens = 200
	acc.totalCostUSD = 1.5
	acc.totalAPICalls = 10
	acc.toolResults = []ToolResultEvent{{ToolName: "test"}}
	recv.mu.Unlock()

	recv.ResetMetrics(testFeatureName)

	recv.mu.RLock()
	defer recv.mu.RUnlock()
	if _, ok := recv.accumulators[testFeatureName]; ok {
		t.Error("ResetMetrics did not remove the feature accumulator")
	}
}

func TestResetMetrics_All(t *testing.T) {
	recv, _ := setupOTELReceiver(t)

	recv.mu.Lock()
	a := recv.getOrCreateAccumulatorLocked("alpha")
	a.totalCostUSD = 1.0
	b := recv.getOrCreateAccumulatorLocked("beta")
	b.totalCostUSD = 2.0
	recv.mu.Unlock()

	recv.ResetMetrics()

	recv.mu.RLock()
	defer recv.mu.RUnlock()
	if len(recv.accumulators) != 0 {
		t.Errorf("expected 0 accumulators after ResetMetrics(), got %d", len(recv.accumulators))
	}
}

// ---------------------------------------------------------------------------
// TestHelpers
// ---------------------------------------------------------------------------

func TestExtractAttributes(t *testing.T) {
	kvs := []*commonpb.KeyValue{
		{Key: "foo", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "hello"}}},
		{Key: "bar", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 42}}},
	}
	attrs := extractAttributes(kvs)
	if attrs["foo"] != "hello" {
		t.Errorf("expected 'hello', got %q", attrs["foo"])
	}
	if attrs["bar"] != "42" {
		t.Errorf("expected '42', got %q", attrs["bar"])
	}
}

func TestAnyValueToString(t *testing.T) {
	tests := []struct {
		name     string
		value    *commonpb.AnyValue
		expected string
	}{
		{"nil", nil, ""},
		{"string", &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "hello"}}, "hello"},
		{"int", &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 42}}, "42"},
		{"bool", &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: true}}, "true"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := anyValueToString(tc.value)
			if got != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestAttrFloat(t *testing.T) {
	attrs := map[string]interface{}{
		"double": 3.14,
		"string": "2.5",
		"empty":  nil,
	}
	if got := attrFloat(attrs, "double"); got != 3.14 {
		t.Errorf("expected 3.14, got %f", got)
	}
	if got := attrFloat(attrs, "string"); got != 2.5 {
		t.Errorf("expected 2.5, got %f", got)
	}
	if got := attrFloat(attrs, "missing"); got != 0 {
		t.Errorf("expected 0, got %f", got)
	}
}

func TestGRPCMetrics_GaugeType(t *testing.T) {
	recv, clients, conn := startTestGRPC(t)
	defer func() {
		conn.Close()
		recv.Stop()
	}()

	ts := uint64(time.Now().UnixNano())
	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				Resource: testResource(testFeatureName),
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{
								Name: "claude_code.cost.usage",
								Data: &metricspb.Metric_Gauge{
									Gauge: &metricspb.Gauge{
										DataPoints: []*metricspb.NumberDataPoint{
											{
												TimeUnixNano: ts,
												Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: 0.25},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := clients.metrics.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	acc := getAcc(t, recv, testFeatureName)
	if acc == nil {
		t.Fatal("expected accumulator for test feature")
	}
	if acc.totalCostUSD != 0.25 {
		t.Errorf("expected totalCostUSD=0.25, got %f", acc.totalCostUSD)
	}
}

// ---------------------------------------------------------------------------
// TestExtractWorkflowFeature
// ---------------------------------------------------------------------------

func TestExtractWorkflowFeature(t *testing.T) {
	tests := []struct {
		name     string
		attrs    []*commonpb.KeyValue
		expected string
	}{
		{
			name:     "present",
			attrs:    testResource("my-feature").Attributes,
			expected: "my-feature",
		},
		{
			name: "absent",
			attrs: []*commonpb.KeyValue{
				{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: otelServiceName}}},
			},
			expected: "",
		},
		{
			name:     "empty",
			attrs:    nil,
			expected: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractWorkflowFeature(tc.attrs)
			if got != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestOTELAccumulatorPartitioning
// ---------------------------------------------------------------------------

func TestOTELAccumulatorPartitioning(t *testing.T) {
	recv, clients, conn := startTestGRPC(t)
	defer func() {
		conn.Close()
		recv.Stop()
	}()

	ts := uint64(time.Now().UnixNano())

	// Send metrics for "alpha" workflow.
	alphaReq := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				Resource: testResource("alpha"),
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{
								Name: "claude_code.token.usage",
								Data: &metricspb.Metric_Sum{
									Sum: &metricspb.Sum{
										DataPoints: []*metricspb.NumberDataPoint{
											{
												TimeUnixNano: ts,
												Value:        &metricspb.NumberDataPoint_AsInt{AsInt: 1000},
												Attributes: []*commonpb.KeyValue{
													{Key: "type", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "input"}}},
												},
											},
										},
									},
								},
							},
							{
								Name: "claude_code.cost.usage",
								Data: &metricspb.Metric_Sum{
									Sum: &metricspb.Sum{
										DataPoints: []*metricspb.NumberDataPoint{
											{
												TimeUnixNano: ts,
												Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: 0.50},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Send metrics for "beta" workflow.
	betaReq := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				Resource: testResource("beta"),
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{
								Name: "claude_code.token.usage",
								Data: &metricspb.Metric_Sum{
									Sum: &metricspb.Sum{
										DataPoints: []*metricspb.NumberDataPoint{
											{
												TimeUnixNano: ts,
												Value:        &metricspb.NumberDataPoint_AsInt{AsInt: 2000},
												Attributes: []*commonpb.KeyValue{
													{Key: "type", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "input"}}},
												},
											},
										},
									},
								},
							},
							{
								Name: "claude_code.cost.usage",
								Data: &metricspb.Metric_Sum{
									Sum: &metricspb.Sum{
										DataPoints: []*metricspb.NumberDataPoint{
											{
												TimeUnixNano: ts,
												Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: 1.25},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	ctx := context.Background()
	if _, err := clients.metrics.Export(ctx, alphaReq); err != nil {
		t.Fatalf("Export alpha failed: %v", err)
	}
	if _, err := clients.metrics.Export(ctx, betaReq); err != nil {
		t.Fatalf("Export beta failed: %v", err)
	}

	// Verify alpha accumulator.
	alphaAcc := getAcc(t, recv, "alpha")
	if alphaAcc == nil {
		t.Fatal("expected accumulator for alpha")
	}
	if alphaAcc.inputTokens != 1000 {
		t.Errorf("alpha: expected inputTokens=1000, got %d", alphaAcc.inputTokens)
	}
	if alphaAcc.totalCostUSD != 0.50 {
		t.Errorf("alpha: expected totalCostUSD=0.50, got %f", alphaAcc.totalCostUSD)
	}

	// Verify beta accumulator.
	betaAcc := getAcc(t, recv, "beta")
	if betaAcc == nil {
		t.Fatal("expected accumulator for beta")
	}
	if betaAcc.inputTokens != 2000 {
		t.Errorf("beta: expected inputTokens=2000, got %d", betaAcc.inputTokens)
	}
	if betaAcc.totalCostUSD != 1.25 {
		t.Errorf("beta: expected totalCostUSD=1.25, got %f", betaAcc.totalCostUSD)
	}

	// Verify GetCostUSD per-feature.
	if cost := recv.GetCostUSD("alpha"); cost != 0.50 {
		t.Errorf("GetCostUSD(alpha): expected 0.50, got %f", cost)
	}
	if cost := recv.GetCostUSD("beta"); cost != 1.25 {
		t.Errorf("GetCostUSD(beta): expected 1.25, got %f", cost)
	}
	// Sum across all.
	if cost := recv.GetCostUSD(); cost != 1.75 {
		t.Errorf("GetCostUSD(): expected 1.75, got %f", cost)
	}
}

// ---------------------------------------------------------------------------
// TestOTELAccumulatorResetIsolation
// ---------------------------------------------------------------------------

func TestOTELAccumulatorResetIsolation(t *testing.T) {
	recv, clients, conn := startTestGRPC(t)
	defer func() {
		conn.Close()
		recv.Stop()
	}()

	ts := uint64(time.Now().UnixNano())
	ctx := context.Background()

	// Send metrics for both alpha and beta.
	for _, feature := range []string{"alpha", "beta"} {
		req := &colmetricspb.ExportMetricsServiceRequest{
			ResourceMetrics: []*metricspb.ResourceMetrics{
				{
					Resource: testResource(feature),
					ScopeMetrics: []*metricspb.ScopeMetrics{
						{
							Metrics: []*metricspb.Metric{
								{
									Name: "claude_code.cost.usage",
									Data: &metricspb.Metric_Sum{
										Sum: &metricspb.Sum{
											DataPoints: []*metricspb.NumberDataPoint{
												{
													TimeUnixNano: ts,
													Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: 0.75},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}
		if _, err := clients.metrics.Export(ctx, req); err != nil {
			t.Fatalf("Export %s failed: %v", feature, err)
		}
	}

	// Reset beta only.
	recv.ResetMetrics("beta")

	// Alpha should be unaffected.
	alphaAcc := getAcc(t, recv, "alpha")
	if alphaAcc == nil {
		t.Fatal("expected alpha accumulator to survive beta reset")
	}
	if alphaAcc.totalCostUSD != 0.75 {
		t.Errorf("alpha: expected totalCostUSD=0.75 after beta reset, got %f", alphaAcc.totalCostUSD)
	}

	// Beta should be gone.
	betaAcc := getAcc(t, recv, "beta")
	if betaAcc != nil {
		t.Error("expected beta accumulator to be removed after reset")
	}

	// GetCostUSD should only reflect alpha now.
	if cost := recv.GetCostUSD(); cost != 0.75 {
		t.Errorf("GetCostUSD() after beta reset: expected 0.75, got %f", cost)
	}
	if cost := recv.GetCostUSD("beta"); cost != 0 {
		t.Errorf("GetCostUSD(beta) after reset: expected 0, got %f", cost)
	}
}

// ---------------------------------------------------------------------------
// TestOTELAttributeRouting
// ---------------------------------------------------------------------------

func TestOTELAttributeRouting(t *testing.T) {
	recv, clients, conn := startTestGRPC(t)
	defer func() {
		conn.Close()
		recv.Stop()
	}()

	ts := uint64(time.Now().UnixNano())
	ctx := context.Background()

	costMetric := func(cost float64) *metricspb.Metric {
		return &metricspb.Metric{
			Name: "claude_code.cost.usage",
			Data: &metricspb.Metric_Sum{
				Sum: &metricspb.Sum{
					DataPoints: []*metricspb.NumberDataPoint{
						{
							TimeUnixNano: ts,
							Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: cost},
						},
					},
				},
			},
		}
	}

	// 1. No workflow.feature attribute — should be silently dropped.
	noFeatureReq := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				Resource: testResource(""), // service.name set but no workflow.feature
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{Metrics: []*metricspb.Metric{costMetric(99.99)}},
				},
			},
		},
	}
	if _, err := clients.metrics.Export(ctx, noFeatureReq); err != nil {
		t.Fatalf("Export (no feature) failed: %v", err)
	}

	// 2. Wrong service.name — should be silently dropped.
	wrongServiceRes := &resourcepb.Resource{
		Attributes: []*commonpb.KeyValue{
			{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "other-service"}}},
			{Key: "workflow.feature", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "should-not-appear"}}},
		},
	}
	wrongServiceReq := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				Resource: wrongServiceRes,
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{Metrics: []*metricspb.Metric{costMetric(88.88)}},
				},
			},
		},
	}
	if _, err := clients.metrics.Export(ctx, wrongServiceReq); err != nil {
		t.Fatalf("Export (wrong service) failed: %v", err)
	}

	// 3. Valid request — should be accumulated.
	validReq := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				Resource: testResource("valid-feature"),
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{Metrics: []*metricspb.Metric{costMetric(0.42)}},
				},
			},
		},
	}
	if _, err := clients.metrics.Export(ctx, validReq); err != nil {
		t.Fatalf("Export (valid) failed: %v", err)
	}

	// Verify: no accumulator for dropped data.
	recv.mu.RLock()
	accCount := len(recv.accumulators)
	_, hasNoFeature := recv.accumulators[""]
	_, hasShouldNot := recv.accumulators["should-not-appear"]
	recv.mu.RUnlock()

	if hasNoFeature {
		t.Error("should not have accumulator for empty feature name")
	}
	if hasShouldNot {
		t.Error("should not have accumulator for wrong service.name")
	}
	if accCount != 1 {
		t.Errorf("expected exactly 1 accumulator (valid-feature), got %d", accCount)
	}

	// Verify the valid accumulator.
	validAcc := getAcc(t, recv, "valid-feature")
	if validAcc == nil {
		t.Fatal("expected accumulator for valid-feature")
	}
	if validAcc.totalCostUSD != 0.42 {
		t.Errorf("expected totalCostUSD=0.42, got %f", validAcc.totalCostUSD)
	}
}
