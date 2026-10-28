package report

import "testing"

func TestSummarizeExitCode(t *testing.T) {
	tests := []struct {
		name       string
		verdicts   []Verdict
		failOnSkip bool
		want       int
	}{
		{"no results", nil, false, 0},
		{"all pass", []Verdict{Pass, Pass}, false, 0},
		{"skip is fine by default", []Verdict{Pass, Skip}, false, 0},
		{"skip fails with --fail-on-skip", []Verdict{Pass, Skip}, true, 1},
		{"only skips prove nothing (D197)", []Verdict{Skip, Skip}, false, 2},
		{"only skips with --fail-on-skip: 1 wins over 2", []Verdict{Skip, Skip}, true, 1},
		{"fail", []Verdict{Pass, Fail}, false, 1},
		{"error only", []Verdict{Pass, Error}, false, 2},
		{"fail wins over error", []Verdict{Error, Fail}, false, 1},
		{"skip and error without the flag", []Verdict{Skip, Error}, false, 2},
		{"skip wins over error with --fail-on-skip", []Verdict{Skip, Error}, true, 1},
		{"a verdict a check bug left empty is an error, never exit 0", []Verdict{Pass, ""}, false, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := make([]Result, len(tt.verdicts))
			for i, v := range tt.verdicts {
				results[i] = Result{ID: "AT-01", Verdict: v}
			}
			if got := Summarize(results, tt.failOnSkip).ExitCode; got != tt.want {
				t.Errorf("exit code = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSummarizeCountsPerResultAndPerCheck(t *testing.T) {
	results := []Result{
		{ID: "AT-04.1", Verdict: Pass},
		{ID: "AT-04.2", Verdict: Fail},
		{ID: "AT-05", Verdict: Skip},
		{ID: "AT-06", Verdict: Error},
	}
	got := Summarize(results, false)
	want := Summary{Pass: 1, Fail: 1, Skip: 1, Error: 1, ExitCode: 1, Results: 4, Checks: 3}
	if got != want {
		t.Errorf("Summarize() = %+v, want %+v", got, want)
	}
}
