package specworkflow

import (
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// Event type constants
// ---------------------------------------------------------------------------

const (
	// EventSpecVersion is emitted when a new spec version is produced.
	EventSpecVersion = "spec_version"
	// EventIssueUpdate is emitted when an issue changes status.
	EventIssueUpdate = "issue_update"
	// EventConvergenceUpdate is emitted after a judge evaluation with convergence metrics.
	EventConvergenceUpdate = "convergence_update"
	// EventGateRequest is emitted when a human gate requires input.
	EventGateRequest = "gate_request"
	// EventCircuitBreaker is emitted when a circuit breaker threshold is approached or tripped.
	EventCircuitBreaker = "circuit_breaker"
	// EventAgentError is emitted when an agent encounters a retryable or fatal error.
	EventAgentError = "agent_error"
	// EventStateTransition is emitted when the workflow transitions between states.
	EventStateTransition = "state_transition"
	// EventAgentDispatch is emitted when an agent is about to be invoked.
	EventAgentDispatch = "agent_dispatch"
	// EventAgentComplete is emitted when an agent invocation finishes (success or failure).
	EventAgentComplete = "agent_complete"
	// EventWorkflowStatus is emitted as a periodic heartbeat with current workflow status.
	EventWorkflowStatus = "workflow_status"
	// EventAgentMetrics is emitted when accumulated agent telemetry metrics are available.
	EventAgentMetrics = "agent_metrics"
	// EventAgentToolEvent is emitted when a tool invocation result is reported via OTEL.
	EventAgentToolEvent = "agent_tool_event"
	// EventAgentAPIEvent is emitted when an API call event is reported via OTEL.
	EventAgentAPIEvent = "agent_api_event"
)

// ---------------------------------------------------------------------------
// Event envelope
// ---------------------------------------------------------------------------

// EventEnvelope is the standard wrapper for all WebSocket events.
// Every event follows the {event: string, data: object} pattern.
type EventEnvelope struct {
	// Event is the event type identifier (e.g. "spec_version").
	Event string `json:"event"`
	// Data is the event-specific payload.
	Data interface{} `json:"data"`
}

// ---------------------------------------------------------------------------
// Event payload structs
// ---------------------------------------------------------------------------

// SpecVersionEvent is the payload for EventSpecVersion.
type SpecVersionEvent struct {
	// Version is the spec version number.
	Version int `json:"version"`
	// Round is the review/revise round in which this version was produced.
	Round int `json:"round"`
	// Timestamp is the ISO 8601 time when the version was created.
	Timestamp string `json:"timestamp"`
	// ChangeSummary describes what changed in this version.
	ChangeSummary string `json:"change_summary"`
	// FilePath is the path to the spec file on disk.
	FilePath string `json:"file_path"`
}

// IssueUpdateEvent is the payload for EventIssueUpdate.
type IssueUpdateEvent struct {
	// IssueID is the unique identifier of the issue.
	IssueID string `json:"issue_id"`
	// Status is the current status of the issue (e.g. "open", "closed").
	Status string `json:"status"`
	// Round is the review/revise round associated with the update.
	Round int `json:"round"`
	// Severity is the severity level of the issue.
	Severity string `json:"severity"`
	// Lens is the review lens that raised the issue.
	Lens string `json:"lens"`
	// Detail provides additional context about the issue.
	Detail string `json:"detail"`
}

// ConvergenceUpdateEvent is the payload for EventConvergenceUpdate.
type ConvergenceUpdateEvent struct {
	// Round is the review/revise round for this convergence snapshot.
	Round int `json:"round"`
	// Verdict is the judge's verdict (e.g. "PASS", "REVISE", "BLOCK").
	Verdict string `json:"verdict"`
	// OpenCritical is the count of open critical findings.
	OpenCritical int `json:"open_critical"`
	// OpenMajor is the count of open major findings.
	OpenMajor int `json:"open_major"`
	// OpenMinor is the count of open minor findings.
	OpenMinor int `json:"open_minor"`
	// Progress indicates whether the spec is converging towards acceptance.
	Progress bool `json:"progress"`
	// Rationale explains the judge's reasoning.
	Rationale string `json:"rationale"`
}

// GateRequestEvent is the payload for EventGateRequest.
type GateRequestEvent struct {
	// GateType identifies the kind of gate (e.g. "requirements_confirmation", "ambiguity_resolution").
	GateType string `json:"gate_type"`
	// TaskID is the identifier of the workflow task awaiting input.
	TaskID string `json:"task_id"`
	// Data carries gate-specific structured data for the human to review.
	Data interface{} `json:"data"`
}

// CircuitBreakerEvent is the payload for EventCircuitBreaker.
type CircuitBreakerEvent struct {
	// Breaker identifies which circuit breaker fired (e.g. "max_rounds", "cost_usd").
	Breaker string `json:"breaker"`
	// Value is the current value that triggered the breaker.
	Value interface{} `json:"value"`
	// Limit is the configured threshold for this breaker.
	Limit interface{} `json:"limit"`
}

// AgentErrorEvent is the payload for EventAgentError.
type AgentErrorEvent struct {
	// Agent is the name of the agent that encountered the error.
	Agent string `json:"agent"`
	// ErrorType classifies the error (e.g. "timeout", "rate_limit", "invalid_output").
	ErrorType string `json:"error_type"`
	// RetryCount is how many retries have been attempted so far.
	RetryCount int `json:"retry_count"`
	// MaxRetries is the maximum number of retries allowed.
	MaxRetries int `json:"max_retries"`
}

// StateTransitionEvent is the payload for EventStateTransition.
type StateTransitionEvent struct {
	// From is the state being left.
	From string `json:"from"`
	// To is the state being entered.
	To string `json:"to"`
	// Round is the current review/revise round.
	Round int `json:"round"`
	// Timestamp is the ISO 8601 time of the transition.
	Timestamp string `json:"timestamp"`
}

// AgentDispatchEvent is the payload for EventAgentDispatch.
type AgentDispatchEvent struct {
	// Agent is the name of the agent being dispatched.
	Agent string `json:"agent"`
	// Round is the current review/revise round.
	Round int `json:"round"`
	// Timestamp is the ISO 8601 time of dispatch.
	Timestamp string `json:"timestamp"`
}

// AgentCompleteEvent is the payload for EventAgentComplete.
type AgentCompleteEvent struct {
	// Agent is the name of the agent that completed.
	Agent string `json:"agent"`
	// Round is the review/revise round in which the agent ran.
	Round int `json:"round"`
	// Success indicates whether the agent completed successfully.
	Success bool `json:"success"`
	// DurationMS is the agent execution time in milliseconds.
	DurationMS int64 `json:"duration_ms"`
	// CostUSD is the estimated cost of this agent invocation.
	CostUSD float64 `json:"cost_usd"`
	// Timestamp is the ISO 8601 time of completion.
	Timestamp string `json:"timestamp"`
}

// WorkflowStatusEvent is the payload for EventWorkflowStatus.
type WorkflowStatusEvent struct {
	// State is the current workflow state name.
	State string `json:"state"`
	// Round is the current review/revise round.
	Round int `json:"round"`
	// Feature is the feature name being specified.
	Feature string `json:"feature_name"`
	// CostUSD is the cumulative cost so far.
	CostUSD float64 `json:"cost_usd"`
	// WallClock is the elapsed wall-clock time in seconds.
	WallClock float64 `json:"wall_clock_seconds"`
	// Invocations is the total number of agent invocations so far.
	Invocations int `json:"agent_invocations"`
	// Timestamp is the ISO 8601 time of the status snapshot.
	Timestamp string `json:"timestamp"`
}

// ---------------------------------------------------------------------------
// EventEmitter interface
// ---------------------------------------------------------------------------

// EventEmitter is the interface for components that broadcast workflow events.
type EventEmitter interface {
	// Emit sends an event envelope to all subscribers. Returns an error if
	// the emitter has been closed or is otherwise unable to accept events.
	Emit(event EventEnvelope) error
}

// ---------------------------------------------------------------------------
// ChannelEmitter
// ---------------------------------------------------------------------------

// ChannelEmitter is a channel-based implementation of EventEmitter.
// It uses a buffered channel so that slow consumers do not block the
// workflow. Events are dropped if the channel is full.
type ChannelEmitter struct {
	ch chan EventEnvelope
}

// NewChannelEmitter creates a new ChannelEmitter with the given buffer size.
func NewChannelEmitter(bufSize int) *ChannelEmitter {
	return &ChannelEmitter{
		ch: make(chan EventEnvelope, bufSize),
	}
}

// ErrChannelFull is returned by ChannelEmitter.Emit when the event channel
// buffer is full and the event cannot be delivered.
var ErrChannelFull = fmt.Errorf("event channel full: event dropped")

// Emit sends an event to the channel. If the channel buffer is full the
// event is dropped and ErrChannelFull is returned so the caller can decide
// how to handle it (e.g. log a warning). Returns nil on success.
func (e *ChannelEmitter) Emit(event EventEnvelope) error {
	select {
	case e.ch <- event:
		return nil
	default:
		return ErrChannelFull
	}
}

// Events returns a read-only channel for consuming emitted events.
func (e *ChannelEmitter) Events() <-chan EventEnvelope {
	return e.ch
}

// Close closes the underlying channel. After Close, no further events
// should be emitted.
func (e *ChannelEmitter) Close() {
	close(e.ch)
}

// ---------------------------------------------------------------------------
// Helper constructors
// ---------------------------------------------------------------------------

// NewSpecVersionEvent creates an EventEnvelope for a new spec version.
func NewSpecVersionEvent(version, round int, filePath string) EventEnvelope {
	return EventEnvelope{
		Event: EventSpecVersion,
		Data: SpecVersionEvent{
			Version:   version,
			Round:     round,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			FilePath:  filePath,
		},
	}
}

// NewIssueUpdateEvent creates an EventEnvelope for an issue status change.
func NewIssueUpdateEvent(issueID, status string, round int, severity, lens string) EventEnvelope {
	return EventEnvelope{
		Event: EventIssueUpdate,
		Data: IssueUpdateEvent{
			IssueID:  issueID,
			Status:   status,
			Round:    round,
			Severity: severity,
			Lens:     lens,
		},
	}
}

// NewConvergenceUpdateEvent creates an EventEnvelope for a convergence status update.
func NewConvergenceUpdateEvent(round int, verdict string, openCritical, openMajor, openMinor int, progress bool, rationale string) EventEnvelope {
	return EventEnvelope{
		Event: EventConvergenceUpdate,
		Data: ConvergenceUpdateEvent{
			Round:        round,
			Verdict:      verdict,
			OpenCritical: openCritical,
			OpenMajor:    openMajor,
			OpenMinor:    openMinor,
			Progress:     progress,
			Rationale:    rationale,
		},
	}
}

// NewGateRequestEvent creates an EventEnvelope for a human gate request.
func NewGateRequestEvent(gateType, taskID string, data interface{}) EventEnvelope {
	return EventEnvelope{
		Event: EventGateRequest,
		Data: GateRequestEvent{
			GateType: gateType,
			TaskID:   taskID,
			Data:     data,
		},
	}
}

// NewCircuitBreakerEvent creates an EventEnvelope for a circuit breaker notification.
func NewCircuitBreakerEvent(breaker string, value, limit interface{}) EventEnvelope {
	return EventEnvelope{
		Event: EventCircuitBreaker,
		Data: CircuitBreakerEvent{
			Breaker: breaker,
			Value:   value,
			Limit:   limit,
		},
	}
}

// NewAgentErrorEvent creates an EventEnvelope for an agent error notification.
func NewAgentErrorEvent(agent, errorType string, retryCount, maxRetries int) EventEnvelope {
	return EventEnvelope{
		Event: EventAgentError,
		Data: AgentErrorEvent{
			Agent:      agent,
			ErrorType:  errorType,
			RetryCount: retryCount,
			MaxRetries: maxRetries,
		},
	}
}

// NewStateTransitionEvent creates an EventEnvelope for a state transition.
func NewStateTransitionEvent(from, to string, round int) EventEnvelope {
	return EventEnvelope{
		Event: EventStateTransition,
		Data: StateTransitionEvent{
			From:      from,
			To:        to,
			Round:     round,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		},
	}
}

// NewAgentDispatchEvent creates an EventEnvelope for an agent dispatch.
func NewAgentDispatchEvent(agent string, round int) EventEnvelope {
	return EventEnvelope{
		Event: EventAgentDispatch,
		Data: AgentDispatchEvent{
			Agent:     agent,
			Round:     round,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		},
	}
}

// NewAgentCompleteEvent creates an EventEnvelope for an agent completion.
func NewAgentCompleteEvent(agent string, round int, success bool, durationMS int64, costUSD float64) EventEnvelope {
	return EventEnvelope{
		Event: EventAgentComplete,
		Data: AgentCompleteEvent{
			Agent:      agent,
			Round:      round,
			Success:    success,
			DurationMS: durationMS,
			CostUSD:    costUSD,
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
		},
	}
}

// NewWorkflowStatusEvent creates an EventEnvelope for a workflow status heartbeat.
func NewWorkflowStatusEvent(state string, round int, feature string, costUSD, wallClock float64, invocations int) EventEnvelope {
	return EventEnvelope{
		Event: EventWorkflowStatus,
		Data: WorkflowStatusEvent{
			State:       state,
			Round:       round,
			Feature:     feature,
			CostUSD:     costUSD,
			WallClock:   wallClock,
			Invocations: invocations,
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
		},
	}
}
