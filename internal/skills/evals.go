package skills

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// EvalCase is one case of a skill's evals.json (skill-creator format, §13.1).
type EvalCase struct {
	ID             int      `json:"id"`
	Prompt         string   `json:"prompt"`
	ExpectedOutput string   `json:"expected_output"`
	Expectations   []string `json:"expectations"`
}

// Evals is a skill's evals.json.
type Evals struct {
	SkillName string     `json:"skill_name"`
	Evals     []EvalCase `json:"evals"`
}

// ErrNoEvals means the skill has no evals.json or no cases.
var ErrNoEvals = errors.New("the skill has no evals")

// ErrBadEvals means evals.json is not valid JSON of the skill-creator format.
var ErrBadEvals = errors.New("evals.json is not valid")

// ParseEvals reads evals.json.
func ParseEvals(b []byte) (Evals, error) {
	var e Evals
	if len(b) == 0 {
		return e, ErrNoEvals
	}
	if err := json.Unmarshal(b, &e); err != nil {
		return e, fmt.Errorf("%w: %w", ErrBadEvals, err)
	}
	var cases []EvalCase
	for _, c := range e.Evals {
		if strings.TrimSpace(c.Prompt) != "" {
			cases = append(cases, c)
		}
	}
	if len(cases) == 0 {
		return e, ErrNoEvals
	}
	e.Evals = cases
	return e, nil
}

// EvalResult is the outcome of one case over its trials. Score is the mean share of
// expectations met; the case passes when at least half of the trials meet them all.
type EvalResult struct {
	ID           int           `json:"id"`
	Prompt       string        `json:"prompt"`
	Output       string        `json:"output"`
	Trials       int           `json:"trials"`
	Passed       int           `json:"passed"`
	Pass         bool          `json:"pass"`
	Score        float64       `json:"score"`
	Expectations []Expectation `json:"expectations"`
	Error        string        `json:"error,omitempty"`
}

// EvalReport is the outcome of all cases of a skill version.
type EvalReport struct {
	Skill   string       `json:"skill"`
	Version int          `json:"version"`
	Score   float64      `json:"score"`
	Pass    bool         `json:"pass"`
	CostUSD float64      `json:"cost_usd"`
	Cases   []EvalResult `json:"cases"`
}

// Expectation is the grade of one expectation.
type Expectation struct {
	Text   string `json:"text"`
	Pass   bool   `json:"pass"`
	Reason string `json:"reason"`
}
