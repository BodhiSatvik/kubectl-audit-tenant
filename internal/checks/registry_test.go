package checks

import (
	"slices"
	"strings"
	"testing"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

var allResultIDs = []string{
	"AT-01", "AT-02", "AT-03",
	"AT-04.1", "AT-04.2", "AT-04.3", "AT-04.4", "AT-04.5",
	"AT-05", "AT-06", "AT-07", "AT-08", "AT-09", "AT-10",
}

func TestRegistryHasTenChecksInOrder(t *testing.T) {
	want := []string{"AT-01", "AT-02", "AT-03", "AT-04", "AT-05", "AT-06", "AT-07", "AT-08", "AT-09", "AT-10"}
	var got []string
	for _, s := range Registry {
		got = append(got, s.ID)
	}
	if !slices.Equal(got, want) {
		t.Errorf("check IDs = %v, want %v", got, want)
	}
}

func TestRegistryHasFourteenResultIDsInOrder(t *testing.T) {
	if got := ResultIDs(Registry); !slices.Equal(got, allResultIDs) {
		t.Errorf("result IDs = %v, want %v", got, allResultIDs)
	}
}

func TestRegistryEntriesAreComplete(t *testing.T) {
	kinds := []report.Kind{report.Behavioral, report.Config, report.Computed}
	severities := []report.Severity{report.High, report.Medium, report.Low}
	for _, s := range Registry {
		if s.Name == "" || s.Method == "" {
			t.Errorf("%s: name and method must be set", s.ID)
		}
		if !slices.Contains(kinds, s.Kind) {
			t.Errorf("%s: kind %q is not behavioral, config or computed", s.ID, s.Kind)
		}
		if !slices.Contains(severities, s.Severity) {
			t.Errorf("%s: severity %q is not high, medium or low", s.ID, s.Severity)
		}
		for _, r := range s.Results {
			if r.Name == "" || r.MTB == "" {
				t.Errorf("%s: result name and MTB must be set (use - for no MTB)", r.ID)
			}
			if !strings.HasPrefix(r.ID, s.ID) {
				t.Errorf("result %s does not belong to check %s", r.ID, s.ID)
			}
		}
	}
}
