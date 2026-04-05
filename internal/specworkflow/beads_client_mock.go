package specworkflow

import (
	"context"
	"sync"
)

// MockCall records a single method invocation on MockBeadsClient.
type MockCall struct {
	Method string
	Args   []interface{}
}

// MockBeadsClient implements BeadsClientInterface for unit testing.
// It records all calls and returns configurable responses.
type MockBeadsClient struct {
	mu    sync.Mutex
	Calls []MockCall

	// Return values for methods. Set these before calling the method.
	CreateEpicResult     string
	CreateEpicErr        error
	CreateFindingResult  string
	CreateFindingErr     error
	UpdateIssueErr       error
	CloseIssueErr        error
	CommentErr           error
	GateCreateResult     string
	GateCreateErr        error
	GateShowResult       []byte
	GateShowErr          error
	GateCloseErr         error
	KVSetErr             error
	KVGetResult          string
	KVGetErr             error
	MolPourResult        string
	MolPourErr           error
	MolShowStepsResult   []byte
	MolShowStepsErr      error
	CloseStepErr         error
	ChildrenResult       []byte
	ChildrenErr          error
	EnsureCustomTypesErr error
	ConfigGetResult      string
	ConfigGetErr         error

	// KVGetFunc, when non-nil, overrides the default KVGetResult/KVGetErr
	// behavior. It receives the key and returns per-key results.
	KVGetFunc func(key string) (string, error)

	// KVStore provides per-key storage. When non-nil, KVSet writes here and
	// KVGet reads from here (before falling back to KVGetFunc/KVGetResult).
	KVStore map[string]string
}

func (m *MockBeadsClient) record(method string, args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, MockCall{Method: method, Args: args})
}

// GetCalls returns a copy of the recorded calls (thread-safe).
func (m *MockBeadsClient) GetCalls() []MockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]MockCall, len(m.Calls))
	copy(out, m.Calls)
	return out
}

func (m *MockBeadsClient) CreateEpic(_ context.Context, actor string, title string) (string, error) {
	m.record("CreateEpic", actor, title)
	return m.CreateEpicResult, m.CreateEpicErr
}

func (m *MockBeadsClient) CreateFinding(_ context.Context, actor string, parentEpicID string, finding MergedFinding, runID string) (string, error) {
	m.record("CreateFinding", actor, parentEpicID, finding, runID)
	return m.CreateFindingResult, m.CreateFindingErr
}

func (m *MockBeadsClient) UpdateIssue(_ context.Context, actor string, beadID string, status string, metadata map[string]string) error {
	m.record("UpdateIssue", actor, beadID, status, metadata)
	return m.UpdateIssueErr
}

func (m *MockBeadsClient) CloseIssue(_ context.Context, actor string, beadID string, reason string, metadata map[string]string) error {
	m.record("CloseIssue", actor, beadID, reason, metadata)
	return m.CloseIssueErr
}

func (m *MockBeadsClient) Comment(_ context.Context, actor string, beadID string, body string) error {
	m.record("Comment", actor, beadID, body)
	return m.CommentErr
}

func (m *MockBeadsClient) GateCreate(_ context.Context, actor string, title string) (string, error) {
	m.record("GateCreate", actor, title)
	return m.GateCreateResult, m.GateCreateErr
}

func (m *MockBeadsClient) GateShow(_ context.Context, actor string, beadID string) ([]byte, error) {
	m.record("GateShow", actor, beadID)
	return m.GateShowResult, m.GateShowErr
}

func (m *MockBeadsClient) GateClose(_ context.Context, actor string, beadID string, reason string) error {
	m.record("GateClose", actor, beadID, reason)
	return m.GateCloseErr
}

func (m *MockBeadsClient) KVSet(_ context.Context, actor string, key string, value string) error {
	m.record("KVSet", actor, key, value)
	if m.KVStore != nil {
		m.mu.Lock()
		m.KVStore[key] = value
		m.mu.Unlock()
	}
	return m.KVSetErr
}

func (m *MockBeadsClient) KVGet(_ context.Context, actor string, key string) (string, error) {
	m.record("KVGet", actor, key)
	if m.KVStore != nil {
		m.mu.Lock()
		v, ok := m.KVStore[key]
		m.mu.Unlock()
		if ok {
			return v, nil
		}
	}
	if m.KVGetFunc != nil {
		return m.KVGetFunc(key)
	}
	return m.KVGetResult, m.KVGetErr
}

func (m *MockBeadsClient) MolPour(_ context.Context, actor string, protoBeadID string, vars map[string]string) (string, error) {
	m.record("MolPour", actor, protoBeadID, vars)
	return m.MolPourResult, m.MolPourErr
}

func (m *MockBeadsClient) MolShowSteps(_ context.Context, actor string, molID string) ([]byte, error) {
	m.record("MolShowSteps", actor, molID)
	return m.MolShowStepsResult, m.MolShowStepsErr
}

func (m *MockBeadsClient) CloseStep(_ context.Context, actor string, stepBeadID string) error {
	m.record("CloseStep", actor, stepBeadID)
	return m.CloseStepErr
}

func (m *MockBeadsClient) Children(_ context.Context, actor string, parentBeadID string) ([]byte, error) {
	m.record("Children", actor, parentBeadID)
	return m.ChildrenResult, m.ChildrenErr
}

func (m *MockBeadsClient) EnsureCustomTypesConfigured(_ context.Context) error {
	m.record("EnsureCustomTypesConfigured")
	return m.EnsureCustomTypesErr
}

func (m *MockBeadsClient) ConfigGet(_ context.Context, key string) (string, error) {
	m.record("ConfigGet", key)
	return m.ConfigGetResult, m.ConfigGetErr
}
