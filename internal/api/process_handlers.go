package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"syscall"

	"github.com/foundry-zero/adversarial-spec-system/internal/process"
)

// maxProcessListLimit is the maximum number of records returned by the list
// endpoint (FR-006).
const maxProcessListLimit = 500

// HandleListProcesses returns an http.HandlerFunc that responds with a JSON
// array of all tracked process records, capped at 500.
//
//	GET /api/processes[?feature=X&status=Y&limit=N]
func HandleListProcesses(tracker *process.ProcessTracker) http.HandlerFunc {
	validStatuses := map[string]bool{
		process.StatusRunning: true,
		process.StatusExited:  true,
		process.StatusKilled:  true,
		process.StatusLost:    true,
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		feature := r.URL.Query().Get("feature")
		status := r.URL.Query().Get("status")

		if status != "" && !validStatuses[status] {
			writeJSONError(w, "invalid status filter: "+status, http.StatusBadRequest)
			return
		}

		// Parse optional limit, capped at maxProcessListLimit.
		limit := maxProcessListLimit
		if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
			if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
				limit = n
			}
		}
		if limit > maxProcessListLimit {
			limit = maxProcessListLimit
		}

		records := tracker.ListFiltered(feature, status, limit)
		if records == nil {
			records = []process.ProcessRecord{}
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(records); err != nil {
			log.Printf("[process-handlers] failed to encode process list: %v", err)
		}
	}
}

// HandleKillProcess returns an http.HandlerFunc that sends a kill signal to
// the process identified by the {pid} path segment.
//
//	POST /api/processes/{pid}/kill
func HandleKillProcess(killSvc *process.KillService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		// Parse PID from path: /api/processes/{pid}/kill
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/processes/"), "/")
		if len(parts) < 2 || parts[1] != "kill" {
			writeJSONError(w, "invalid path", http.StatusBadRequest)
			return
		}

		pid, err := strconv.Atoi(parts[0])
		if err != nil {
			writeJSONError(w, "invalid pid: must be an integer", http.StatusBadRequest)
			return
		}

		if err := killSvc.Kill(r.Context(), pid); err != nil {
			switch {
			case errors.Is(err, process.ErrInvalidPID):
				writeJSONError(w, err.Error(), http.StatusBadRequest)
			case errors.Is(err, process.ErrNotFound):
				writeJSONError(w, fmt.Sprintf("process %d not found", pid), http.StatusNotFound)
			case errors.Is(err, process.ErrAlreadyTerminated):
				writeJSONError(w, fmt.Sprintf("process %d already ended", pid), http.StatusConflict)
			case errors.Is(err, syscall.EPERM):
				writeJSONError(w, fmt.Sprintf("permission denied: process %d not owned by this server", pid), http.StatusForbidden)
			case errors.Is(err, syscall.ESRCH):
				writeJSONError(w, "process already ended", http.StatusConflict)
			default:
				writeJSONError(w, "kill failed: "+err.Error(), http.StatusInternalServerError)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"kill_accepted"}`))
	}
}

// writeJSONError writes a JSON error response.
func writeJSONError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
