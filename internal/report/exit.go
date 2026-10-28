package report

import "strings"

// Summary counts results and carries the exit code (ADR 0004).
type Summary struct {
	Pass     int `json:"pass"`
	Fail     int `json:"fail"`
	Skip     int `json:"skip"`
	Error    int `json:"error"`
	ExitCode int `json:"exitCode"`
	Results  int `json:"results"`
	Checks   int `json:"checks"`
}

// Summarize counts per result and per check family. With failOnSkip a SKIP counts as a failure.
// Exit code: 1 on any FAIL (or any SKIP with failOnSkip), else 2 on any ERROR or when every
// result is SKIP (nothing was proven, D197), else 0.
func Summarize(results []Result, failOnSkip bool) Summary {
	s := Summary{Results: len(results)}
	families := map[string]struct{}{}
	for _, r := range results {
		switch r.Verdict {
		case Pass:
			s.Pass++
		case Fail:
			s.Fail++
		case Skip:
			s.Skip++
		default: // Error, or a verdict a check bug left empty: never let it read as exit 0
			s.Error++
		}
		family, _, _ := strings.Cut(r.ID, ".")
		families[family] = struct{}{}
	}
	s.Checks = len(families)
	switch {
	case s.Fail > 0 || (failOnSkip && s.Skip > 0):
		s.ExitCode = 1
	case s.Error > 0 || s.NothingProven():
		s.ExitCode = 2
	}
	return s
}

// NothingProven reports that every result is SKIP, as when -n, --as or --as-group is misspelled (D197).
func (s Summary) NothingProven() bool { return s.Skip > 0 && s.Skip == s.Results }
