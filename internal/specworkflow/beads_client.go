package specworkflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
)

// ErrBeadsUnavailable is returned when the bd binary is not found on $PATH.
var ErrBeadsUnavailable = errors.New("beads: bd not found on PATH")

// BeadsClientInterface defines the typed interface for all Beads operations.
// All methods accept context.Context as the first parameter. Write methods
// accept an actor string parameter to set --actor=<role>.
type BeadsClientInterface interface {
	// CreateEpic creates a top-level epic issue. Returns the bead ID.
	CreateEpic(ctx context.Context, actor string, title string) (string, error)
	// CreateFinding creates a finding issue under a parent epic. Returns the bead ID.
	CreateFinding(ctx context.Context, actor string, parentEpicID string, finding MergedFinding, runID string) (string, error)
	// UpdateIssue updates an issue's status. metadata is an optional map of
	// key=value pairs passed as --metadata (e.g. issue_status=addressed).
	UpdateIssue(ctx context.Context, actor string, beadID string, status string, metadata map[string]string) error
	// CloseIssue closes an issue with an optional reason. metadata is an
	// optional map of key=value pairs passed as --metadata.
	CloseIssue(ctx context.Context, actor string, beadID string, reason string, metadata map[string]string) error
	// Comment posts a comment on an issue.
	Comment(ctx context.Context, actor string, beadID string, body string) error
	// GateCreate creates a task-type issue as a gate proxy. Returns the bead ID.
	GateCreate(ctx context.Context, actor string, title string) (string, error)
	// GateShow returns the JSON output of bd show for a gate task.
	GateShow(ctx context.Context, actor string, beadID string) ([]byte, error)
	// GateClose closes a gate proxy task with a reason.
	GateClose(ctx context.Context, actor string, beadID string, reason string) error
	// KVSet sets a key-value pair in bd kv.
	KVSet(ctx context.Context, actor string, key string, value string) error
	// KVGet retrieves a value from bd kv. Returns the value and any error.
	KVGet(ctx context.Context, actor string, key string) (string, error)
	// MolPour pours a molecule from a proto. Returns the molecule bead ID.
	MolPour(ctx context.Context, actor string, protoBeadID string, vars map[string]string) (string, error)
	// MolShowSteps returns the JSON output of bd mol show.
	MolShowSteps(ctx context.Context, actor string, molID string) ([]byte, error)
	// CloseStep closes a molecule step.
	CloseStep(ctx context.Context, actor string, stepBeadID string) error
	// Children returns the JSON output of bd children for a parent bead.
	Children(ctx context.Context, actor string, parentBeadID string) ([]byte, error)
	// EnsureCustomTypesConfigured ensures the "finding" custom type is registered.
	EnsureCustomTypesConfigured(ctx context.Context) error
	// ConfigGet retrieves a config value by key.
	ConfigGet(ctx context.Context, key string) (string, error)
}

// BeadsClient implements BeadsClientInterface by executing bd as a subprocess.
// All calls are serialized via a sync.Mutex to prevent Dolt write conflicts.
type BeadsClient struct {
	mu           sync.Mutex
	workspaceDir string
	bdPath       string // resolved path to bd binary
}

// NewBeadsClient creates a new BeadsClient. It resolves the bd binary path
// immediately and returns ErrBeadsUnavailable if bd is not on PATH.
func NewBeadsClient(workspaceDir string) (*BeadsClient, error) {
	bdPath, err := exec.LookPath("bd")
	if err != nil {
		return nil, ErrBeadsUnavailable
	}
	return &BeadsClient{
		workspaceDir: workspaceDir,
		bdPath:       bdPath,
	}, nil
}

// run executes a bd command with the given arguments, serialized via mutex.
// It returns the trimmed stdout. On non-zero exit, it returns a wrapped error
// containing stderr.
func (c *BeadsClient) run(ctx context.Context, args ...string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	cmd := exec.CommandContext(ctx, c.bdPath, args...)
	cmd.Dir = c.workspaceDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("bd %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// errEmptyBeadID is returned when bd create exits 0 but produces no bead ID.
var errEmptyBeadID = fmt.Errorf("bd create: empty bead ID returned (exit 0)")

// runCreate executes a bd create command with one retry on empty stdout (EC-02).
// If the first attempt returns an empty bead ID (exit 0), it retries once.
// Non-zero exit errors are NOT retried.
func (c *BeadsClient) runCreate(ctx context.Context, args ...string) (string, error) {
	id, err := c.run(ctx, args...)
	if err != nil {
		return "", err
	}
	if id == "" {
		// Retry once per EC-02.
		id, err = c.run(ctx, args...)
		if err != nil {
			return "", err
		}
		if id == "" {
			return "", errEmptyBeadID
		}
	}
	return id, nil
}

// createArgs returns common flags for bd create operations (--silent, --actor, --dolt-auto-commit).
func createArgs(actor string) []string {
	return []string{"--actor=" + actor, "--dolt-auto-commit=on", "--silent"}
}

// readArgs returns common flags for read operations.
func readArgs(actor string) []string {
	return []string{"--actor=" + actor}
}

func (c *BeadsClient) CreateEpic(ctx context.Context, actor string, title string) (string, error) {
	args := []string{"create"}
	args = append(args, createArgs(actor)...)
	args = append(args, "--type=epic", title)
	return c.runCreate(ctx, args...)
}

func (c *BeadsClient) CreateFinding(ctx context.Context, actor string, parentEpicID string, finding MergedFinding, runID string) (string, error) {
	metadata := fmt.Sprintf("severity=%s,lens=%s,round=%d,affected_section=%s,raised_by=%s,run_id=%s",
		finding.Severity, finding.Lens, finding.RoundRaised, finding.AffectedSection,
		strings.Join(finding.RaisedBy, ";"), runID)
	if finding.ConstitutionPrinciple != nil {
		metadata += ",constitution_principle=" + *finding.ConstitutionPrinciple
	}

	args := []string{"create"}
	args = append(args, createArgs(actor)...)
	args = append(args, "--type=finding", "--parent", parentEpicID, "--metadata", metadata, finding.Description)
	return c.runCreate(ctx, args...)
}

func (c *BeadsClient) UpdateIssue(ctx context.Context, actor string, beadID string, status string, metadata map[string]string) error {
	args := []string{"update", "--actor=" + actor, "--dolt-auto-commit=on", "--status", status}
	if len(metadata) > 0 {
		args = append(args, "--metadata", formatMetadata(metadata))
	}
	args = append(args, beadID)
	_, err := c.run(ctx, args...)
	return err
}

func (c *BeadsClient) CloseIssue(ctx context.Context, actor string, beadID string, reason string, metadata map[string]string) error {
	args := []string{"close", "--actor=" + actor, "--dolt-auto-commit=on"}
	if reason != "" {
		args = append(args, "--reason", reason)
	}
	if len(metadata) > 0 {
		args = append(args, "--metadata", formatMetadata(metadata))
	}
	args = append(args, beadID)
	_, err := c.run(ctx, args...)
	return err
}

// formatMetadata formats a map as comma-separated key=value pairs for --metadata.
func formatMetadata(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(m))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, ",")
}

func (c *BeadsClient) Comment(ctx context.Context, actor string, beadID string, body string) error {
	args := []string{"comment", "--actor=" + actor, "--dolt-auto-commit=on", beadID, body}
	_, err := c.run(ctx, args...)
	return err
}

func (c *BeadsClient) GateCreate(ctx context.Context, actor string, title string) (string, error) {
	args := []string{"create"}
	args = append(args, createArgs(actor)...)
	args = append(args, "--type=task", title)
	return c.runCreate(ctx, args...)
}

func (c *BeadsClient) GateShow(ctx context.Context, actor string, beadID string) ([]byte, error) {
	args := []string{"show"}
	args = append(args, readArgs(actor)...)
	args = append(args, "--json", beadID)
	out, err := c.run(ctx, args...)
	return []byte(out), err
}

func (c *BeadsClient) GateClose(ctx context.Context, actor string, beadID string, reason string) error {
	args := []string{"close", "--actor=" + actor, "--dolt-auto-commit=on", "--reason", reason, beadID}
	_, err := c.run(ctx, args...)
	return err
}

func (c *BeadsClient) KVSet(ctx context.Context, actor string, key string, value string) error {
	args := []string{"kv", "set", "--actor=" + actor, "--dolt-auto-commit=on", key, value}
	_, err := c.run(ctx, args...)
	return err
}

func (c *BeadsClient) KVGet(ctx context.Context, actor string, key string) (string, error) {
	args := []string{"kv", "get"}
	args = append(args, readArgs(actor)...)
	args = append(args, key)
	return c.run(ctx, args...)
}

func (c *BeadsClient) MolPour(ctx context.Context, actor string, protoBeadID string, vars map[string]string) (string, error) {
	args := []string{"mol", "pour"}
	args = append(args, createArgs(actor)...)
	args = append(args, protoBeadID)
	for k, v := range vars {
		args = append(args, "--var", k+"="+v)
	}
	return c.runCreate(ctx, args...)
}

func (c *BeadsClient) MolShowSteps(ctx context.Context, actor string, molID string) ([]byte, error) {
	args := []string{"mol", "show"}
	args = append(args, readArgs(actor)...)
	args = append(args, "--json", molID)
	out, err := c.run(ctx, args...)
	return []byte(out), err
}

func (c *BeadsClient) CloseStep(ctx context.Context, actor string, stepBeadID string) error {
	args := []string{"close", "--actor=" + actor, "--dolt-auto-commit=on", stepBeadID}
	_, err := c.run(ctx, args...)
	return err
}

func (c *BeadsClient) Children(ctx context.Context, actor string, parentBeadID string) ([]byte, error) {
	args := []string{"children"}
	args = append(args, readArgs(actor)...)
	args = append(args, "--json", parentBeadID)
	out, err := c.run(ctx, args...)
	return []byte(out), err
}

func (c *BeadsClient) EnsureCustomTypesConfigured(ctx context.Context) error {
	current, err := c.ConfigGet(ctx, "types.custom")
	if err != nil {
		// Key absent or error — set to "finding" directly.
		return c.configSet(ctx, "types.custom", "finding")
	}
	if current == "" {
		return c.configSet(ctx, "types.custom", "finding")
	}
	// Check if "finding" is already present in the comma-separated list.
	for _, t := range strings.Split(current, ",") {
		if strings.TrimSpace(t) == "finding" {
			return nil
		}
	}
	// Append "finding" to existing types.
	return c.configSet(ctx, "types.custom", current+",finding")
}

func (c *BeadsClient) ConfigGet(ctx context.Context, key string) (string, error) {
	return c.run(ctx, "config", "get", key)
}

// configSet is an internal helper for bd config set.
func (c *BeadsClient) configSet(ctx context.Context, key, value string) error {
	_, err := c.run(ctx, "config", "set", key, value)
	return err
}
