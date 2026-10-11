// Package report holds the result model that every check writes and every printer reads.
package report

import "time"

// Verdict is the outcome of one result (ADR 0004).
type Verdict string

// The four verdicts.
const (
	Pass  Verdict = "PASS"
	Fail  Verdict = "FAIL"
	Skip  Verdict = "SKIP"  // the tool chose not to decide; always with a reason and a hint
	Error Verdict = "ERROR" // the tool could not complete the check
)

// Kind says how a check decides: it asks the cluster, reads objects, or computes.
type Kind string

// The three kinds.
const (
	Behavioral Kind = "behavioral"
	Config     Kind = "config"
	Computed   Kind = "computed"
)

// Severity is static per check.
type Severity string

// The three severities.
const (
	High   Severity = "high"
	Medium Severity = "medium"
	Low    Severity = "low"
)

// Result is one line of the report. Hint is empty only on PASS.
type Result struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Kind     Kind     `json:"kind"`
	MTB      string   `json:"mtb"` // "-" when the check has no MTB ID
	Severity Severity `json:"severity"`
	Verdict  Verdict  `json:"verdict"`
	Layer    string   `json:"layer"` // RBAC, admission (...), config, or "-"
	Reason   string   `json:"reason"`
	Hint     string   `json:"hint"`
}

// Identity is what the API server said the caller is (ADR 0009).
type Identity struct {
	Mode     string   `json:"mode"` // self or impersonation
	Username string   `json:"username"`
	Groups   []string `json:"groups"`
	Verified bool     `json:"verified"`
}

// Cluster names the server the run talked to.
type Cluster struct {
	Context string `json:"context"`
	Server  string `json:"server"`
	Version string `json:"version"`
}

// Tool names the binary that produced the report.
type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Report is the full output of one run.
type Report struct {
	Tool           Tool      `json:"tool"`
	Cluster        Cluster   `json:"cluster"`
	Namespace      string    `json:"namespace"`
	OtherNamespace string    `json:"otherNamespace"`
	Identity       Identity  `json:"identity"`
	Timestamp      time.Time `json:"timestamp"`
	Results        []Result  `json:"results"`
	Summary        Summary   `json:"summary"`
	// AdminHint is set in self mode when AT-03 finds the cluster-wide wildcard allowed.
	AdminHint bool `json:"-"`
	// FailOnSkip records the --fail-on-skip flag for the summary wording.
	FailOnSkip bool `json:"-"`
}
