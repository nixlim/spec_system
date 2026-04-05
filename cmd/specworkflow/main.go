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
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/foundry-zero/adversarial-spec-system/internal/api"
	"github.com/foundry-zero/adversarial-spec-system/internal/codedoc"
	"github.com/foundry-zero/adversarial-spec-system/internal/codereview"
	"github.com/foundry-zero/adversarial-spec-system/internal/process"
	"github.com/foundry-zero/adversarial-spec-system/internal/specworkflow"
	"gopkg.in/yaml.v3"
)

func main() {
	port := flag.Int("port", 8080, "HTTP listen port")
	workspace := flag.String("workspace", "./workspace", "workspace directory for spec files and uploads")
	configPath := flag.String("config", "", "path to YAML configuration file (optional)")
	otelPort := flag.Int("otel-port", 4317, "gRPC OTLP receiver port for Claude Code telemetry (0 to disable)")
	flag.Parse()

	// Set up server log ring buffer so all log.Printf output is captured.
	logBuffer := api.NewLogBuffer(500)
	logWriters := []io.Writer{os.Stderr, logBuffer}

	// Open a persistent log file in the workspace directory so the full
	// server log survives beyond the ring buffer's 500-line window.
	absWS, _ := filepath.Abs(*workspace)
	_ = os.MkdirAll(absWS, 0755)
	logFilePath := filepath.Join(absWS, "server.log")
	logFile, logFileErr := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if logFileErr != nil {
		fmt.Fprintf(os.Stderr, "WARNING: could not open log file %s: %v\n", logFilePath, logFileErr)
	} else {
		logWriters = append(logWriters, logFile)
		defer logFile.Close()
	}
	log.SetOutput(io.MultiWriter(logWriters...))

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
	crConfig := codereview.DefaultCodeReviewConfig()
	cdConfig := codedoc.DefaultCodedocConfig()
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

		// Parse code_review section from the same YAML file.
		var wrapper struct {
			CodeReview codereview.CodeReviewConfig `yaml:"code_review"`
		}
		wrapper.CodeReview = crConfig // start from defaults
		if yamlErr := yaml.Unmarshal(data, &wrapper); yamlErr == nil {
			if err := wrapper.CodeReview.Validate(); err != nil {
				log.Fatalf("failed to validate code_review config: %v", err)
			}
			crConfig = wrapper.CodeReview
		}

		// Parse codedoc section from the same YAML file.
		var cdWrapper struct {
			Codedoc codedoc.CodedocConfig `yaml:"codedoc"`
		}
		cdWrapper.Codedoc = cdConfig // start from defaults
		if yamlErr := yaml.Unmarshal(data, &cdWrapper); yamlErr == nil {
			if err := cdWrapper.Codedoc.Validate(); err != nil {
				log.Fatalf("failed to validate codedoc config: %v", err)
			}
			cdConfig = cdWrapper.Codedoc
		}
	}
	config, err = resolveStartupSpecConfig(config)
	if err != nil {
		log.Fatalf("spec workflow startup configuration invalid: %v", err)
	}

	log.Printf("[codedoc] config loaded (max_rounds=%d, mode=%s, cost_budget=$%.2f)",
		cdConfig.MaxRounds, cdConfig.DefaultMode, cdConfig.MaxCostUSD)

	// --- Create shared infrastructure ---
	emitter := specworkflow.NewChannelEmitter(64)
	wsHub := api.NewWebSocketHub()
	workflowManager := api.NewWorkflowManager(emitter, wsHub, absWorkspace, config)

	// Configure OTEL port for child process telemetry.
	if *otelPort > 0 {
		workflowManager.SetOTELPort(*otelPort)
	}

	// --- Open SQLite metrics store for persistent telemetry ---
	metricsDBPath := filepath.Join(absWorkspace, "metrics.db")
	metricsStore, err := api.NewMetricsStore(metricsDBPath)
	if err != nil {
		log.Fatalf("failed to open metrics store: %v", err)
	}
	defer metricsStore.Close()
	workflowManager.SetMetricsStore(metricsStore)
	log.Printf("[main] metrics store opened at %s", metricsDBPath)

	// --- Open process tracking store (shares the metrics DB file) ---
	processDB, err := sql.Open("sqlite", metricsDBPath)
	if err != nil {
		log.Fatalf("failed to open process tracking db: %v", err)
	}
	defer processDB.Close()
	processStore, err := process.NewSQLiteStore(processDB)
	if err != nil {
		log.Fatalf("failed to create process store: %v", err)
	}

	// Bridge EventEmitFunc → ChannelEmitter so process events reach WebSocket clients.
	processEmitFn := func(eventType, featureName string, data interface{}) {
		_ = emitter.Emit(specworkflow.EventEnvelope{
			Event:       eventType,
			FeatureName: featureName,
			Data:        data,
		})
	}
	processTracker := process.NewProcessTracker(processStore, processEmitFn)
	killEscalationTimeout := time.Duration(config.KillEscalationTimeoutSeconds) * time.Second
	killService := process.NewKillService(process.RealSignalSender{}, processTracker, killEscalationTimeout)

	// Recover stale "running" records from a prior server crash/shutdown.
	lostRecords, err := processStore.MarkLostOnStartup(context.Background())
	if err != nil {
		log.Fatalf("[process-tracker] failed to mark lost on startup: %v", err)
	}
	for _, rec := range lostRecords {
		processEmitFn("process_lost", rec.Feature, process.ProcessLostEvent{
			Type:    "process_lost",
			PID:     rec.PID,
			Feature: rec.Feature,
			Role:    rec.Role,
		})
		log.Printf("[process-tracker] marked PID %d as lost (feature=%s, role=%s)", rec.PID, rec.Feature, rec.Role)
	}

	// Wire process tracker into workflow manager so runners get it.
	workflowManager.SetProcessTracker(processTracker)

	// Start WebSocket broadcast goroutine.
	go wsHub.StartBroadcasting(emitter)

	// --- Start OTLP gRPC receiver (for Claude Code telemetry) ---
	var otelReceiver *api.OTELReceiver
	if *otelPort > 0 {
		otelReceiver = api.NewOTELReceiver(wsHub, emitter)
		otelReceiver.SetMetricsStore(metricsStore, workflowManager.GetCurrentFeatureName)
		// Restore ALL in-memory accumulators from SQLite so counters
		// don't reset to zero after a server restart.
		otelReceiver.RestoreFromStore()
		workflowManager.SetOTELReceiver(otelReceiver)
		if err := otelReceiver.Start(*otelPort); err != nil {
			log.Fatalf("failed to start OTEL gRPC receiver: %v", err)
		}
		defer otelReceiver.Stop()
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
	mux.HandleFunc("/api/workspace/features/", api.HandleFeatureFiles(absWorkspace, workflowManager))
	mux.HandleFunc("/api/workspace/features", api.HandleListFeatures(absWorkspace, workflowManager))

	// --- Workflow control endpoints (NEW) ---
	mux.HandleFunc("/api/workflow/start", api.HandleStartWorkflow(workflowManager))
	mux.HandleFunc("/api/workflow/cancel", api.HandleCancelWorkflowAPI(workflowManager))
	mux.HandleFunc("/api/workflow/status", api.HandleGetWorkflowStatus(workflowManager))
	mux.HandleFunc("/api/workflow/agents", api.HandleGetWorkflowAgents(workflowManager))
	mux.HandleFunc("/api/tasks/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[routing] %s %s", r.Method, r.URL.Path)
		handleTaskRouting(workflowManager).ServeHTTP(w, r)
	})

	// --- Process tracking endpoints ---
	mux.HandleFunc("/api/processes", api.HandleListProcesses(processTracker))
	mux.HandleFunc("/api/processes/", api.HandleKillProcess(killService))

	// --- Workflow control endpoints (retry/reset/restart/resume/rewind) ---
	mux.HandleFunc("/api/workflow/retry", api.HandleRetryWorkflow(workflowManager))
	mux.HandleFunc("/api/workflow/finalize", api.HandleFinalizeWorkflow(workflowManager))
	mux.HandleFunc("/api/workflow/reset", api.HandleResetWorkflow(workflowManager))
	mux.HandleFunc("/api/workflow/restart", api.HandleRestartWorkflow(workflowManager))
	mux.HandleFunc("/api/workflow/resume", api.HandleResumeWorkflow(workflowManager))
	mux.HandleFunc("/api/workflow/rewind", api.HandleRewindWorkflow(workflowManager))
	mux.HandleFunc("/api/workflow/replay", api.HandleReplayPhase(workflowManager))

	// --- Workflow sub-routing for /api/workflow/{feature}/source-docs ---
	// This catch-all for /api/workflow/ only fires for paths NOT matched by the
	// more specific routes above (start, cancel, status, retry, etc).
	mux.HandleFunc("/api/workflow/", handleWorkflowSubRouting(workflowManager))

	// --- Code review endpoints ---
	crManager := api.NewCodeReviewManager(absWorkspace, crConfig)
	// Wire per-role runners and detect Codex CLI availability.
	crReviewRunner := specworkflow.DefaultClaudeRunner(absWorkspace, *otelPort, "", crConfig.ClaudeModels.For("reviewer"))
	crReviewRunner.Tracker = processTracker
	crReviewRunner.Role = "reviewer"
	crFixRunner := specworkflow.DefaultClaudeRunner(absWorkspace, *otelPort, "", crConfig.ClaudeModels.For("fixer"))
	crFixRunner.Tracker = processTracker
	crFixRunner.Role = "fixer"
	var crCodexRunner specworkflow.AgentRunner
	if _, codexErr := exec.LookPath("codex"); codexErr == nil {
		cr := specworkflow.DefaultCodexRunner("", absWorkspace, specworkflow.ReviewerOutputSchema())
		cr.Tracker = processTracker
		cr.Role = "reviewer"
		crCodexRunner = cr
		log.Printf("[codereview] codex CLI detected — dual-provider code review enabled")
	}
	crManager.SetRunners(crReviewRunner, crCodexRunner, crFixRunner)
	crManager.SetEmitter(emitter)
	crManager.SetOTELPort(*otelPort)
	crAuditLogger, err := codereview.NewCRAuditLogger(absWorkspace)
	if err != nil {
		log.Printf("[codereview] WARNING: failed to open audit logger: %v", err)
	} else {
		defer crAuditLogger.Close()
		crManager.SetAuditLogger(crAuditLogger)
	}
	workflowManager.SetCodeReviewManager(crManager)
	mux.HandleFunc("/api/codereview/start", api.HandleCRStart(crManager))
	mux.HandleFunc("/api/codereview/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/gate"):
			api.HandleCRGate(crManager).ServeHTTP(w, r)
		case strings.HasSuffix(path, "/status"):
			api.HandleCRStatus(crManager).ServeHTTP(w, r)
		case strings.HasSuffix(path, "/cancel"):
			api.HandleCRCancel(crManager).ServeHTTP(w, r)
		case strings.HasSuffix(path, "/resume"):
			api.HandleCRResume(crManager).ServeHTTP(w, r)
		case strings.HasSuffix(path, "/reset"):
			api.HandleCRReset(crManager).ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	// --- Codedoc workflow endpoints ---
	cdManager := api.NewCodedocManager(absWorkspace, cdConfig)
	// Per-role runners: reviewer/drafter/discovery share a base runner; merge uses its own.
	cdRunner := specworkflow.DefaultClaudeRunner(absWorkspace, *otelPort, "", cdConfig.ClaudeModels.Default)
	cdRunner.Tracker = processTracker
	cdMergeRunner := specworkflow.DefaultClaudeRunner(absWorkspace, *otelPort, "", cdConfig.ClaudeModels.For("merge"))
	cdMergeRunner.Tracker = processTracker
	cdMergeRunner.Role = "merge"
	var cdCodexRunner specworkflow.AgentRunner
	if _, codexErr := exec.LookPath("codex"); codexErr == nil {
		cd := specworkflow.DefaultCodexRunner("", absWorkspace, nil)
		cd.Tracker = processTracker
		cdCodexRunner = cd
		log.Printf("[codedoc] codex CLI detected — dual-provider codedoc enabled")
	}
	cdManager.SetRunners(cdRunner, cdCodexRunner, cdMergeRunner)
	cdManager.SetEmitter(&api.CDEmitterAdapter{Emitter: emitter})
	workflowManager.SetCodedocManager(cdManager)
	mux.HandleFunc("/api/codedoc/start", api.HandleCDStart(cdManager))
	mux.HandleFunc("/api/codedoc/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/gate"):
			api.HandleCDGate(cdManager).ServeHTTP(w, r)
		case strings.HasSuffix(path, "/status"):
			api.HandleCDStatus(cdManager).ServeHTTP(w, r)
		case strings.HasSuffix(path, "/cancel"):
			api.HandleCDCancel(cdManager).ServeHTTP(w, r)
		case strings.HasSuffix(path, "/resume"):
			api.HandleCDResume(cdManager).ServeHTTP(w, r)
		case strings.HasSuffix(path, "/reset"):
			api.HandleCDReset(cdManager).ServeHTTP(w, r)
		case strings.HasSuffix(path, "/rewind"):
			api.HandleCDRewind(cdManager).ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	// --- Metrics endpoint (persisted OTEL telemetry) ---
	mux.HandleFunc("/api/metrics", api.HandleGetMetrics(metricsStore))

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
		GetTracker: func(featureName ...string) *specworkflow.IssueTracker {
			return workflowManager.GetTracker(featureName...)
		},
		GetState: func(featureName ...string) *specworkflow.WorkflowStateJSON {
			return workflowManager.GetState(featureName...)
		},
		CancelFunc: func() error { return workflowManager.CancelWorkflow() },
	}
	mux.HandleFunc("/api/spec/current", api.HandleGetCurrentSpec(specConfig))
	mux.HandleFunc("/api/spec/versions", api.HandleListSpecVersions(specConfig))
	mux.HandleFunc("/api/spec/version/", api.HandleGetSpecVersion(specConfig))
	mux.HandleFunc("/api/spec/diff/", api.HandleGetSpecDiff(specConfig))
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
	// Serve favicon from static dir, or return 204 No Content if missing.
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(staticDir, "favicon.ico")
		if _, err := os.Stat(path); err == nil {
			http.ServeFile(w, r, path)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
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
	fmt.Printf("  Log file:  %s\n", logFilePath)
	fmt.Printf("  Static:    %s\n", staticDir)
	if *configPath != "" {
		fmt.Printf("  Config:    %s\n", *configPath)
	}
	if *otelPort > 0 {
		fmt.Printf("  OTLP gRPC: localhost:%d\n", *otelPort)
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
	fmt.Printf("  POST /api/workflow/{f}/source-docs  Assign source docs to workflow\n")
	fmt.Printf("  GET  /api/workflow/{f}/source-docs  List workflow source docs\n")
	fmt.Printf("  POST /api/tasks/{id}/approve   Approve a gate task\n")
	fmt.Printf("  POST /api/tasks/{id}/reject    Reject a gate task\n")
	fmt.Printf("  GET  /api/processes             List tracked processes\n")
	fmt.Printf("  POST /api/processes/{pid}/kill  Kill a running agent\n")
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

// handleWorkflowSubRouting routes /api/workflow/{feature}/source-docs
// to the appropriate handlers based on the HTTP method.
// It only handles paths that contain a feature name segment; paths like
// /api/workflow/start are matched by more-specific ServeMux entries first.
func handleWorkflowSubRouting(manager *api.WorkflowManager) http.HandlerFunc {
	assignHandler := api.HandleAssignSourceDocs(manager)
	listDocsHandler := api.HandleGetWorkflowSourceDocs(manager)

	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasSuffix(path, "/source-docs") {
			switch r.Method {
			case http.MethodPost:
				assignHandler.ServeHTTP(w, r)
			case http.MethodGet:
				listDocsHandler.ServeHTTP(w, r)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}
		http.NotFound(w, r)
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

func resolveStartupSpecConfig(cfg specworkflow.SpecWorkflowConfig) (specworkflow.SpecWorkflowConfig, error) {
	resolved := cfg
	detected := detectDefaultSkillPaths()
	if resolved.SkillPaths.PlanSpec == "" {
		resolved.SkillPaths.PlanSpec = detected.PlanSpec
	}
	if resolved.SkillPaths.GrillSpec == "" {
		resolved.SkillPaths.GrillSpec = detected.GrillSpec
	}
	if resolved.SkillPaths.PlanSpec == "" || resolved.SkillPaths.GrillSpec == "" {
		return resolved, fmt.Errorf("missing required spec workflow skills: set skill_paths.plan_spec and skill_paths.grill_spec, or install plan-spec and grill-spec under ~/.agents/skills")
	}
	if err := resolved.Validate(); err != nil {
		return resolved, err
	}
	return resolved, nil
}

func detectDefaultSkillPaths() specworkflow.SkillPaths {
	roots := []string{
		".agents/skills",
		".claude/skills",
	}
	if homeDir, err := os.UserHomeDir(); err == nil {
		roots = append([]string{
			filepath.Join(homeDir, ".agents", "skills"),
			filepath.Join(homeDir, ".codex", "skills"),
		}, roots...)
	}
	for _, root := range roots {
		planSpec := filepath.Join(root, "plan-spec")
		grillSpec := filepath.Join(root, "grill-spec")
		if isDir(planSpec) && isDir(grillSpec) {
			return specworkflow.SkillPaths{
				PlanSpec:  planSpec,
				GrillSpec: grillSpec,
			}
		}
	}
	return specworkflow.SkillPaths{}
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
