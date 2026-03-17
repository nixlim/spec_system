package specworkflow

import (
	"fmt"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// BreakerConfig
// ---------------------------------------------------------------------------

// BreakerConfig holds the configurable limits for all circuit breakers.
type BreakerConfig struct {
	// MaxRounds is the maximum number of review/revise iterations allowed.
	MaxRounds int
	// MaxTotalFindings is the maximum cumulative findings before the breaker trips.
	MaxTotalFindings int
	// StalenessThreshold is the number of consecutive rounds a CRITICAL or MAJOR
	// finding may remain in the same status before the staleness breaker trips.
	StalenessThreshold int
	// MaxWallClockMinutes is the maximum elapsed wall-clock time in minutes.
	// This is a soft check — only evaluated before agent dispatch.
	MaxWallClockMinutes int
	// MaxCostUSD is the maximum cumulative cost in USD before the cost breaker trips.
	MaxCostUSD float64
}

// ---------------------------------------------------------------------------
// BreakerResult
// ---------------------------------------------------------------------------

// BreakerResult captures the outcome of a single circuit-breaker check.
type BreakerResult struct {
	// Triggered is true when the breaker condition has been met.
	Triggered bool
	// BreakerName is the human-readable identifier for this breaker.
	BreakerName string
	// CurrentValue is the current metric value that was evaluated.
	CurrentValue interface{}
	// Limit is the configured threshold for this breaker.
	Limit interface{}
	// Message is a human-readable explanation (populated when Triggered is true).
	Message string
}

// ---------------------------------------------------------------------------
// Individual breaker checks
// ---------------------------------------------------------------------------

// CheckMaxRounds checks whether the current round exceeds the configured maximum.
// Rounds are 1-indexed: rounds 1 through maxRounds are allowed; round maxRounds+1
// triggers the breaker.
func CheckMaxRounds(round, maxRounds int) BreakerResult {
	triggered := round > maxRounds
	r := BreakerResult{
		Triggered:    triggered,
		BreakerName:  "max_rounds",
		CurrentValue: round,
		Limit:        maxRounds,
	}
	if triggered {
		r.Message = fmt.Sprintf("round %d exceeds maximum of %d", round, maxRounds)
	}
	return r
}

// CheckMaxFindings checks whether the cumulative finding count exceeds the
// configured maximum. Exactly maxTotalFindings is allowed; maxTotalFindings+1
// triggers the breaker.
func CheckMaxFindings(cumulativeFindings, maxTotalFindings int) BreakerResult {
	triggered := cumulativeFindings > maxTotalFindings
	r := BreakerResult{
		Triggered:    triggered,
		BreakerName:  "max_findings",
		CurrentValue: cumulativeFindings,
		Limit:        maxTotalFindings,
	}
	if triggered {
		r.Message = fmt.Sprintf("cumulative findings %d exceeds maximum of %d", cumulativeFindings, maxTotalFindings)
	}
	return r
}

// CheckStaleness checks whether any CRITICAL or MAJOR finding has been stuck in
// the same status for at least threshold consecutive rounds. Severity is
// determined by the finding ID prefix: "CRIT-" for critical, "MAJ-" for major.
// Returns a triggered result with the first stale finding ID in Message.
func CheckStaleness(issueHistory map[string][]string, threshold int) BreakerResult {
	r := BreakerResult{
		BreakerName:  "staleness",
		CurrentValue: 0,
		Limit:        threshold,
	}

	for id, statuses := range issueHistory {
		// Only check CRITICAL and MAJOR findings.
		upper := strings.ToUpper(id)
		if !strings.HasPrefix(upper, "CRIT-") && !strings.HasPrefix(upper, "MAJ-") {
			continue
		}

		if len(statuses) < threshold {
			continue
		}

		// Check the last `threshold` statuses for sameness.
		tail := statuses[len(statuses)-threshold:]
		allSame := true
		for i := 1; i < len(tail); i++ {
			if tail[i] != tail[0] {
				allSame = false
				break
			}
		}
		if allSame {
			r.Triggered = true
			r.CurrentValue = len(tail)
			r.Message = fmt.Sprintf("finding %s has been in status %q for %d consecutive rounds", id, tail[0], len(tail))
			return r
		}
	}

	return r
}

// CheckWallClock checks whether the elapsed time since startedAt exceeds
// maxWallClockMinutes. startedAt must be in ISO 8601 / RFC 3339 format.
// This is a soft breaker — it is only intended to be checked before agent
// dispatch, not to abort in-flight work.
func CheckWallClock(startedAt string, maxWallClockMinutes int) BreakerResult {
	r := BreakerResult{
		BreakerName: "wall_clock",
		Limit:       maxWallClockMinutes,
	}

	t, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		// If we can't parse the timestamp, report it but don't trigger.
		r.CurrentValue = startedAt
		r.Message = fmt.Sprintf("unable to parse startedAt %q: %v", startedAt, err)
		return r
	}

	elapsed := time.Since(t)
	elapsedMinutes := int(elapsed.Minutes())
	r.CurrentValue = elapsedMinutes

	if elapsed >= time.Duration(maxWallClockMinutes)*time.Minute {
		r.Triggered = true
		r.Message = fmt.Sprintf("elapsed %d minutes exceeds maximum of %d", elapsedMinutes, maxWallClockMinutes)
	}

	return r
}

// CheckCost checks whether the cumulative cost meets or exceeds the configured
// maximum cost in USD.
func CheckCost(cumulativeCost, maxCostUSD float64) BreakerResult {
	triggered := cumulativeCost >= maxCostUSD
	r := BreakerResult{
		Triggered:    triggered,
		BreakerName:  "cost",
		CurrentValue: cumulativeCost,
		Limit:        maxCostUSD,
	}
	if triggered {
		r.Message = fmt.Sprintf("cumulative cost $%.4f meets or exceeds limit of $%.4f", cumulativeCost, maxCostUSD)
	}
	return r
}

// ---------------------------------------------------------------------------
// Aggregate check
// ---------------------------------------------------------------------------

// CheckAllBreakers runs all five circuit-breaker checks against the given
// workflow state, configuration, and issue history. It returns only the
// breakers that were triggered (i.e. whose conditions were met).
func CheckAllBreakers(state *WorkflowStateJSON, config BreakerConfig, issueHistory map[string][]string) []BreakerResult {
	checks := []BreakerResult{
		CheckMaxRounds(state.Round, config.MaxRounds),
		CheckMaxFindings(state.FindingsSummary.Raised, config.MaxTotalFindings),
		CheckStaleness(issueHistory, config.StalenessThreshold),
		CheckWallClock(state.StartedAt, config.MaxWallClockMinutes),
		CheckCost(state.CumulativeCostUSD, config.MaxCostUSD),
	}

	var triggered []BreakerResult
	for _, c := range checks {
		if c.Triggered {
			triggered = append(triggered, c)
		}
	}
	return triggered
}
