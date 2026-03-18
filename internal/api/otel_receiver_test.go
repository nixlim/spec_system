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
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

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

	recv.mu.Lock()
	defer recv.mu.Unlock()
	if recv.inputTokens != 1000 {
		t.Errorf("expected inputTokens=1000, got %d", recv.inputTokens)
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

	recv.mu.Lock()
	defer recv.mu.Unlock()
	if recv.totalCostUSD != 0.05 {
		t.Errorf("expected totalCostUSD=0.05, got %f", recv.totalCostUSD)
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

	recv.mu.Lock()
	defer recv.mu.Unlock()
	if recv.inputTokens != 500 {
		t.Errorf("expected inputTokens=500, got %d", recv.inputTokens)
	}
	if recv.outputTokens != 200 {
		t.Errorf("expected outputTokens=200, got %d", recv.outputTokens)
	}
	if recv.cacheReadTokens != 300 {
		t.Errorf("expected cacheReadTokens=300, got %d", recv.cacheReadTokens)
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

	recv.mu.Lock()
	defer recv.mu.Unlock()
	if recv.totalCostUSD != 0.10 {
		t.Errorf("expected totalCostUSD=0.10, got %f", recv.totalCostUSD)
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

	recv.mu.Lock()
	defer recv.mu.Unlock()
	if recv.totalAPICalls != 1 {
		t.Errorf("expected totalAPICalls=1, got %d", recv.totalAPICalls)
	}
	if recv.totalCostUSD != 0.07 {
		t.Errorf("expected totalCostUSD=0.07, got %f", recv.totalCostUSD)
	}
	if recv.inputTokens != 2000 {
		t.Errorf("expected inputTokens=2000, got %d", recv.inputTokens)
	}
	if recv.outputTokens != 500 {
		t.Errorf("expected outputTokens=500, got %d", recv.outputTokens)
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

	recv.mu.Lock()
	defer recv.mu.Unlock()
	if len(recv.toolResults) != 1 {
		t.Fatalf("expected 1 tool result, got %d", len(recv.toolResults))
	}
	if recv.toolResults[0].ToolName != "Write" {
		t.Errorf("expected tool_name 'Write', got %q", recv.toolResults[0].ToolName)
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

	recv.mu.Lock()
	defer recv.mu.Unlock()
	if recv.totalCostUSD != 0.25 {
		t.Errorf("expected totalCostUSD=0.25, got %f", recv.totalCostUSD)
	}
}
