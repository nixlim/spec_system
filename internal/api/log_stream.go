// Package api provides HTTP handlers for the adversarial spec system.
// This file implements log streaming endpoints: server log ring buffer
// and workflow JSONL message aggregation.
package api

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// LogBuffer — thread-safe ring buffer for server log lines
// ---------------------------------------------------------------------------

// LogBuffer is a fixed-capacity ring buffer that stores recent log lines.
// It is safe for concurrent use.
type LogBuffer struct {
	mu    sync.Mutex
	lines []string
	cap   int
	pos   int // next write position
	full  bool
}

// NewLogBuffer creates a LogBuffer with the given capacity.
func NewLogBuffer(capacity int) *LogBuffer {
	if capacity < 1 {
		capacity = 500
	}
	return &LogBuffer{
		lines: make([]string, capacity),
		cap:   capacity,
	}
}

// Add appends a line to the ring buffer.
func (b *LogBuffer) Add(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines[b.pos] = line
	b.pos++
	if b.pos >= b.cap {
		b.pos = 0
		b.full = true
	}
}

// Lines returns all buffered lines in chronological order (oldest first).
func (b *LogBuffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.full {
		result := make([]string, b.pos)
		copy(result, b.lines[:b.pos])
		return result
	}

	// Ring has wrapped: entries from pos..cap are oldest, then 0..pos-1.
	result := make([]string, b.cap)
	copy(result, b.lines[b.pos:])
	copy(result[b.cap-b.pos:], b.lines[:b.pos])
	return result
}

// Write implements io.Writer so LogBuffer can be used with log.SetOutput
// via io.MultiWriter. Each Write call is treated as one log line (trailing
// newline stripped).
func (b *LogBuffer) Write(p []byte) (n int, err error) {
	line := strings.TrimRight(string(p), "\n")
	b.Add(line)
	return len(p), nil
}

// ---------------------------------------------------------------------------
// HandleGetServerLogs — GET /api/logs/server
// ---------------------------------------------------------------------------

// HandleGetServerLogs returns an HTTP handler that serves the last N lines
// from the server log ring buffer as a JSON array of strings.
func HandleGetServerLogs(buf *LogBuffer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		lines := buf.Lines()
		// Return last 200 lines (most recent).
		const maxLines = 200
		if len(lines) > maxLines {
			lines = lines[len(lines)-maxLines:]
		}

		writeJSON(w, http.StatusOK, lines)
	}
}

// ---------------------------------------------------------------------------
// HandleGetMessages — GET /api/messages
// ---------------------------------------------------------------------------

// HandleGetMessages returns an HTTP handler that reads all workflow-log.jsonl
// files from workspace/specs/*/ and returns them as a sorted JSON array.
// Accepts optional ?feature=NAME to filter to a specific feature.
func HandleGetMessages(workspaceDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		featureFilter := r.URL.Query().Get("feature")
		specsDir := filepath.Join(workspaceDir, "specs")

		entries, err := os.ReadDir(specsDir)
		if err != nil {
			// No specs directory yet — return empty array.
			writeJSON(w, http.StatusOK, []interface{}{})
			return
		}

		var allMessages []map[string]interface{}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if featureFilter != "" && entry.Name() != featureFilter {
				continue
			}

			logPath := filepath.Join(specsDir, entry.Name(), "workflow-log.jsonl")
			messages := readJSONLFile(logPath)
			allMessages = append(allMessages, messages...)
		}

		// Sort by timestamp.
		sort.Slice(allMessages, func(i, j int) bool {
			ti, _ := allMessages[i]["timestamp"].(string)
			tj, _ := allMessages[j]["timestamp"].(string)
			return ti < tj
		})

		if allMessages == nil {
			allMessages = []map[string]interface{}{}
		}

		writeJSON(w, http.StatusOK, allMessages)
	}
}

// readJSONLFile reads a JSONL file and returns each line as a map.
func readJSONLFile(path string) []map[string]interface{} {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var entries []map[string]interface{}
	scanner := bufio.NewScanner(f)
	// Allow up to 1MB per line for large log entries.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry map[string]interface{}
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // skip malformed lines
		}
		entries = append(entries, entry)
	}
	return entries
}
