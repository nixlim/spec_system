// Command specworkflow starts the adversarial spec review system HTTP server.
// It serves the dashboard UI, API endpoints for spec upload/viewing/diffing/
// issue tracking/convergence metrics, AND workflow control endpoints that
// wire the Orchestrator to the frontend via HTTP + WebSocket.
//
// Usage:
//
//	specworkflow [--port PORT] [--workspace DIR] [--config FILE]
//
// Flags:
//
//	--port       HTTP listen port (default: 8080)
//	--workspace  Workspace directory for spec files and uploads (default: ./workspace)
//	--config     Path to YAML configuration file (optional; uses defaults if omitted)
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/foundry-zero/adversarial-spec-system/internal/api"
	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
)

func main() {
	port := flag.Int("port", 8080, "HTTP listen port")
	workspace := flag.String("workspace", "./workspace", "workspace directory for spec files and uploads")
	configPath := flag.String("config", "", "path to YAML configuration file (optional)")
	otelPort := flag.Int("otel-port", 4318, "OTLP HTTP receiver port for child process telemetry (0 to disable)")
	flag.Parse()

	// Set up server log ring buffer so all log.Printf output is captured.
	logBuffer := api.NewLogBuffer(500)
	log.SetOutput(io.MultiWriter(os.Stderr, logBuffer))

	// Resolve workspace to absolute path.
	absWorkspace, err := filepath.Abs(*workspace)
	if err != nil {
		log.Fatalf("failed to resolve workspace path: %v", err)
	}

	// Ensure workspace directory exists.
	if err := os.MkdirAll(absWorkspace, 0755); err != nil {
		log.Fatalf("failed to create workspace directory: %v", err)
	}

	// Ensure source-docs subdirectory exists for uploads.
	sourceDocsDir := filepath.Join(absWorkspace, "source-docs")
	if err := os.MkdirAll(sourceDocsDir, 0755); err != nil {
		log.Fatalf("failed to create source-docs directory: %v", err)
	}

	// --- Load configuration ---
	config := specworkflow.DefaultConfig()
	if *configPath != "" {
		data, err := os.ReadFile(*configPath)
		if err != nil {
			log.Fatalf("failed to read config file: %v", err)
		}
		parsed, err := specworkflow.ParseConfig(data)
		if err != nil {
			log.Fatalf("failed to parse config: %v", err)
		}
		config = *parsed
	}

	// --- Create shared infrastructure ---
	emitter := specworkflow.NewChannelEmitter(64)
	wsHub := api.NewWebSocketHub()
	workflowManager := api.NewWorkflowManager(emitter, wsHub, absWorkspace, config)

	// Configure OTEL port for child process telemetry.
	if *otelPort > 0 {
		workflowManager.SetOTELPort(*otelPort)
	}

	// Start WebSocket broadcast goroutine.
	go wsHub.StartBroadcasting(emitter)

	// --- Start OTLP HTTP receiver (for child Claude process telemetry) ---
	var otelReceiver *api.OTELReceiver
	if *otelPort > 0 {
		otelReceiver = api.NewOTELReceiver(wsHub, emitter)
		otelMux := http.NewServeMux()
		otelMux.HandleFunc("/v1/metrics", otelReceiver.HandleMetrics)
		otelMux.HandleFunc("/v1/logs", otelReceiver.HandleLogs)
		otelMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			log.Printf("[otel] catch-all: %s %s (content-type: %s)", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("{}"))
		})
		go func() {
			otelAddr := fmt.Sprintf(":%d", *otelPort)
			log.Printf("OTLP receiver listening on %s", otelAddr)
			if err := http.ListenAndServe(otelAddr, otelMux); err != nil {
				log.Printf("OTLP receiver error: %v", err)
			}
		}()
	}

	mux := http.NewServeMux()

	// --- Upload endpoints ---
	uploadConfig := api.UploadConfig{
		WorkspaceDir: absWorkspace,
	}
	mux.HandleFunc("/api/upload", api.HandleUpload(uploadConfig))
	mux.HandleFunc("/api/uploads", api.HandleListUploads(uploadConfig))

	// Also support the paths the frontend uses.
	mux.HandleFunc("/api/workspace/upload", api.HandleUpload(uploadConfig))
	mux.HandleFunc("/api/workspace/uploads", api.HandleListUploads(uploadConfig))

	// --- Workspace browser endpoints ---
	mux.HandleFunc("/api/workspace/features/", api.HandleFeatureFiles(absWorkspace))
	mux.HandleFunc("/api/workspace/features", api.HandleListFeatures(absWorkspace))

	// --- Workflow control endpoints (NEW) ---
	mux.HandleFunc("/api/workflow/start", api.HandleStartWorkflow(workflowManager))
	mux.HandleFunc("/api/workflow/cancel", api.HandleCancelWorkflowAPI(workflowManager))
	mux.HandleFunc("/api/workflow/status", api.HandleGetWorkflowStatus(workflowManager))
	mux.HandleFunc("/api/tasks/", handleTaskRouting(workflowManager))

	// --- Workflow control endpoints (retry/reset) ---
	mux.HandleFunc("/api/workflow/retry", api.HandleRetryWorkflow(workflowManager))
	mux.HandleFunc("/api/workflow/reset", api.HandleResetWorkflow(workflowManager))

	// --- Log / message streaming endpoints ---
	mux.HandleFunc("/api/messages", api.HandleGetMessages(absWorkspace))
	mux.HandleFunc("/api/logs/server", api.HandleGetServerLogs(logBuffer))

	// --- WebSocket endpoint ---
	mux.Handle("/ws", api.HandleWebSocket(wsHub))

	// --- Spec API endpoints ---
	// Wire spec endpoints to live state from the WorkflowManager so they
	// reflect the running orchestrator's data when a workflow is active.
	specConfig := api.SpecAPIConfig{
		WorkspaceDir: absWorkspace,
		FeatureName:  "adversarial-spec",
		GetTracker:   workflowManager.GetTracker,
		GetState:     workflowManager.GetState,
		CancelFunc:   workflowManager.CancelWorkflow,
	}
	mux.HandleFunc("/api/spec/current", api.HandleGetCurrentSpec(specConfig))
	mux.HandleFunc("/api/spec/versions", api.HandleListSpecVersions(specConfig))
	mux.HandleFunc("/api/spec/version/", api.HandleGetSpecVersion(specConfig))
	mux.HandleFunc("/api/spec/diff", api.HandleGetSpecDiff(specConfig))
	mux.HandleFunc("/api/spec/issues", api.HandleGetIssues(specConfig))
	mux.HandleFunc("/api/spec/issues/", api.HandleGetIssue(specConfig))
	mux.HandleFunc("/api/spec/convergence", api.HandleGetConvergence(specConfig))
	mux.HandleFunc("/api/spec/cancel", api.HandleCancelWorkflow(specConfig))

	// Legacy endpoints (keep for backward compatibility).
	mux.HandleFunc("/api/issues", api.HandleGetIssues(specConfig))
	mux.HandleFunc("/api/issue/", api.HandleGetIssue(specConfig))
	mux.HandleFunc("/api/convergence", api.HandleGetConvergence(specConfig))
	mux.HandleFunc("/api/cancel", api.HandleCancelWorkflow(specConfig))

	// --- Static files (dashboard UI) ---
	staticDir := findStaticDir()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))
	// Serve index.html at root.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(staticDir, "index.html"))
	})

	_ = otelReceiver // used above; suppress unused warning when otel disabled

	addr := fmt.Sprintf(":%d", *port)
	fmt.Printf("Adversarial Spec System\n")
	fmt.Printf("  Dashboard: http://localhost:%d\n", *port)
	fmt.Printf("  Workspace: %s\n", absWorkspace)
	fmt.Printf("  Static:    %s\n", staticDir)
	if *configPath != "" {
		fmt.Printf("  Config:    %s\n", *configPath)
	}
	if *otelPort > 0 {
		fmt.Printf("  OTLP:      http://localhost:%d\n", *otelPort)
	}
	fmt.Printf("\nEndpoints:\n")
	fmt.Printf("  GET  /api/workspace/features           List workspace features\n")
	fmt.Printf("  GET  /api/workspace/features/{n}/discovery  Feature discovery output\n")
	fmt.Printf("  GET  /api/workspace/features/{n}/state      Feature workflow state\n")
	fmt.Printf("  GET  /api/workspace/features/{n}/files/{f}  Feature file by name\n")
	fmt.Printf("  POST /api/workflow/start      Start a new workflow\n")
	fmt.Printf("  POST /api/workflow/cancel      Cancel running workflow\n")
	fmt.Printf("  GET  /api/workflow/status      Poll workflow status\n")
	fmt.Printf("  POST /api/workflow/retry       Clear stale workflow state\n")
	fmt.Printf("  POST /api/workflow/reset       Delete feature directory\n")
	fmt.Printf("  POST /api/tasks/{id}/approve   Approve a gate task\n")
	fmt.Printf("  POST /api/tasks/{id}/reject    Reject a gate task\n")
	fmt.Printf("  GET  /ws                       WebSocket event stream\n")
	fmt.Printf("  GET  /api/messages             Workflow log messages\n")
	fmt.Printf("  GET  /api/logs/server          Server log (ring buffer)\n")
	fmt.Printf("  POST /api/upload               Upload source documents\n")
	fmt.Printf("  GET  /api/uploads              List uploaded files\n")
	fmt.Printf("  GET  /api/spec/current         Current spec version\n")
	fmt.Printf("  GET  /api/spec/versions        List spec versions\n")
	fmt.Printf("  GET  /api/spec/version/N       Get specific version\n")
	fmt.Printf("  GET  /api/spec/diff            Diff two versions\n")
	fmt.Printf("  GET  /api/spec/issues          List issues\n")
	fmt.Printf("  GET  /api/spec/issues/ID       Get issue details\n")
	fmt.Printf("  GET  /api/spec/convergence     Convergence metrics\n")
	fmt.Printf("  POST /api/spec/cancel          Cancel workflow (legacy)\n")
	fmt.Printf("\nListening on %s...\n", addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// handleTaskRouting routes /api/tasks/{id}/approve and /api/tasks/{id}/reject
// to the appropriate handlers. Go 1.22+ ServeMux supports patterns, but we
// use a simple prefix-based router for broader compatibility.
func handleTaskRouting(manager *api.WorkflowManager) http.HandlerFunc {
	approveHandler := api.HandleGateApprove(manager)
	rejectHandler := api.HandleGateReject(manager)

	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case hasSuffix(path, "/approve"):
			approveHandler.ServeHTTP(w, r)
		case hasSuffix(path, "/reject"):
			rejectHandler.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	}
}

// hasSuffix checks if path ends with suffix.
func hasSuffix(path, suffix string) bool {
	return len(path) >= len(suffix) && path[len(path)-len(suffix):] == suffix
}

// findStaticDir returns the path to the static/ directory. It checks
// relative to the binary location first, then falls back to ./static.
func findStaticDir() string {
	// Try relative to executable.
	exe, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "static")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}

	// Try ./static (for development).
	if info, err := os.Stat("static"); err == nil && info.IsDir() {
		return "static"
	}

	// Fall back — the FileServer will return 404 for missing files.
	log.Println("WARNING: static/ directory not found; dashboard will not be served")
	return "static"
}
