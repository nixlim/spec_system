package specworkflow

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
)

// JSONValidator validates raw JSON bytes and returns a list of human-readable
// error strings. An empty slice means the JSON is valid.
type JSONValidator func(data []byte) []string

// ValidateAndRetryConfig configures the validation+retry loop for JSON-producing agents.
type ValidateAndRetryConfig struct {
	// AgentName is used for logging and event emission.
	AgentName string
	// MaxAttempts is the total number of dispatch attempts (including the first).
	MaxAttempts int
	// OutputPath is the file path where the agent writes its JSON output.
	OutputPath string
	// TimeoutSeconds is the per-attempt agent timeout.
	TimeoutSeconds int
	// Validator validates the output JSON and returns errors.
	Validator JSONValidator
	// Runner is the agent execution backend.
	Runner AgentRunner
	// BuildPrompt constructs the prompt for each attempt. It receives any
	// validation errors from the previous attempt (nil on first attempt).
	BuildPrompt func(validationErrors []string) string
}

// ValidateAndRetryResult holds the outcome of a validation+retry loop.
type ValidateAndRetryResult struct {
	// Data is the validated output data (nil if all attempts failed).
	Data []byte
	// TotalCost is the cumulative cost across all attempts.
	TotalCost float64
	// TotalDuration is the cumulative duration across all attempts.
	TotalDuration int64
	// Attempts is the number of dispatch attempts made.
	Attempts int
	// LastErrors contains the validation errors from the final failed attempt
	// (nil if the last attempt succeeded).
	LastErrors []string
}

// RunWithValidation dispatches an agent and validates its JSON output,
// retrying with validation error feedback on failure. This implements the
// taskval pattern: dispatch → validate → feed errors back → retry.
//
// On success, returns the validated data. On exhausting all attempts,
// returns the last set of validation errors so the caller can decide
// whether to escalate or fall back.
func RunWithValidation(cfg ValidateAndRetryConfig) (*ValidateAndRetryResult, error) {
	result := &ValidateAndRetryResult{}
	var lastErrors []string

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		result.Attempts = attempt
		log.Printf("[json-validation] %s attempt %d/%d", cfg.AgentName, attempt, cfg.MaxAttempts)

		prompt := cfg.BuildPrompt(lastErrors)

		_, _, cost, duration, runErr := cfg.Runner.Run(prompt, cfg.OutputPath, cfg.TimeoutSeconds)
		result.TotalCost += cost
		result.TotalDuration += duration

		if runErr != nil {
			log.Printf("[json-validation] %s dispatch failed on attempt %d: %v", cfg.AgentName, attempt, runErr)
			lastErrors = []string{fmt.Sprintf("agent dispatch failed: %v", runErr)}
			continue
		}

		// Read the output file.
		data, readErr := os.ReadFile(cfg.OutputPath)
		if readErr != nil {
			log.Printf("[json-validation] %s output file missing on attempt %d: %v", cfg.AgentName, attempt, readErr)
			lastErrors = []string{"output file was not created by the agent"}
			continue
		}

		// Basic JSON validity check.
		if !json.Valid(data) {
			log.Printf("[json-validation] %s produced invalid JSON on attempt %d", cfg.AgentName, attempt)
			// Try to extract JSON from text (agent may have wrapped in commentary).
			if extracted := extractJSONFromText(string(data)); extracted != nil {
				data = extracted
				log.Printf("[json-validation] %s: extracted JSON from text (%d bytes)", cfg.AgentName, len(data))
				// Overwrite the file with clean JSON.
				_ = os.WriteFile(cfg.OutputPath, data, 0o644)
			} else {
				preview := string(data)
				if len(preview) > 200 {
					preview = preview[:200] + "..."
				}
				lastErrors = []string{fmt.Sprintf("output is not valid JSON (starts with: %s)", preview[:min(60, len(preview))])}
				continue
			}
		}

		// Run the domain-specific validator.
		if cfg.Validator != nil {
			validationErrors := cfg.Validator(data)
			if len(validationErrors) > 0 {
				log.Printf("[json-validation] %s validation failed on attempt %d: %v", cfg.AgentName, attempt, validationErrors)
				lastErrors = validationErrors
				continue
			}
		}

		// Validation passed.
		log.Printf("[json-validation] %s output valid on attempt %d", cfg.AgentName, attempt)
		result.Data = data
		result.LastErrors = nil
		return result, nil
	}

	// All attempts exhausted.
	result.LastErrors = lastErrors
	log.Printf("[json-validation] %s failed after %d attempts: %s",
		cfg.AgentName, cfg.MaxAttempts, strings.Join(lastErrors, "; "))
	return result, nil
}

// AppendValidationErrorsToPrompt appends a validation errors section to a
// prompt string. Returns the original prompt unchanged if errors is nil/empty.
func AppendValidationErrorsToPrompt(prompt string, errors []string) string {
	if len(errors) == 0 {
		return prompt
	}

	var b strings.Builder
	b.WriteString(prompt)
	b.WriteString("\n\n## Validation Errors (from previous attempt)\n\n")
	b.WriteString("Your previous attempt failed validation. Fix ALL of these errors:\n\n")
	for _, e := range errors {
		fmt.Fprintf(&b, "- %s\n", e)
	}
	b.WriteString("\nProduce corrected output that resolves every error listed above.\n")
	return b.String()
}

// DiscoveryOutputValidator returns a JSONValidator for DiscoveryOutput.
func DiscoveryOutputValidator() JSONValidator {
	return func(data []byte) []string {
		var output DiscoveryOutput
		if err := json.Unmarshal(data, &output); err != nil {
			return []string{fmt.Sprintf("invalid JSON structure: %v", err)}
		}
		errs := ValidateDiscoveryOutput(&output)
		if len(errs) == 0 {
			return nil
		}
		result := make([]string, len(errs))
		for i, e := range errs {
			result[i] = e.Error()
		}
		return result
	}
}

// HoldoutOutputValidator returns a JSONValidator for HoldoutOutput.
func HoldoutOutputValidator(expectedRound int, expectedHoldoutFile string) JSONValidator {
	return func(data []byte) []string {
		var output HoldoutOutput
		if err := json.Unmarshal(data, &output); err != nil {
			return []string{fmt.Sprintf("invalid JSON structure: %v", err)}
		}
		errs := ValidateHoldoutOutput(&output, expectedRound, expectedHoldoutFile)
		if len(errs) == 0 {
			return nil
		}
		result := make([]string, len(errs))
		for i, e := range errs {
			result[i] = e.Error()
		}
		return result
	}
}
