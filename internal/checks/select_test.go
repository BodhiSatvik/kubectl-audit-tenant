package checks

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// implemented gives every registry entry a Run for the duration of the test, so selection can
// be tested over all ten checks before their tasks land.
func implemented(t *testing.T) {
	t.Helper()
	saved := Registry
	Registry = slices.Clone(Registry)
	for i := range Registry {
		Registry[i].Run = func(context.Context, *cluster.Client, Spec, func(report.Result)) []report.Result { return nil }
	}
	t.Cleanup(func() { Registry = saved })
}

func TestSelect(t *testing.T) {
	implemented(t)
	without := func(ids ...string) []string {
		return slices.DeleteFunc(slices.Clone(allResultIDs), func(id string) bool { return slices.Contains(ids, id) })
	}
	tests := []struct {
		name    string
		include []string
		skip    []string
		want    []string
	}{
		{"no flags keeps every result", nil, nil, allResultIDs},
		{"a family selects its sub-checks", []string{"AT-04"}, nil, []string{"AT-04.1", "AT-04.2", "AT-04.3", "AT-04.4", "AT-04.5"}},
		{"a sub-check alone", []string{"AT-04.2"}, nil, []string{"AT-04.2"}},
		{"--skip removes from the kept set", []string{"AT-01", "AT-04"}, []string{"AT-04.3"}, []string{"AT-01", "AT-04.1", "AT-04.2", "AT-04.4", "AT-04.5"}},
		{"--skip alone removes one result", nil, []string{"AT-04.2"}, without("AT-04.2")},
		{"--skip a family removes all its results", nil, []string{"AT-04"}, without("AT-04.1", "AT-04.2", "AT-04.3", "AT-04.4", "AT-04.5")},
		{"order follows the registry, not the flag", []string{"AT-05", "AT-01"}, nil, []string{"AT-01", "AT-05"}},
		{"a space after the comma is fine", []string{"AT-01", " AT-05"}, nil, []string{"AT-01", "AT-05"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			specs, err := Select(tt.include, tt.skip)
			if err != nil {
				t.Fatalf("Select() error = %v", err)
			}
			if got := ResultIDs(specs); !slices.Equal(got, tt.want) {
				t.Errorf("Select() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSelectRejectsChecksNotInThisBuild(t *testing.T) {
	for _, include := range [][]string{{"AT-06"}, {"AT-01", "AT-06"}, {"AT-01", "AT-10"}} {
		if _, err := Select(include, nil); err == nil || !strings.Contains(err.Error(), "is not in this build yet") {
			t.Errorf("Select(%v) error = %v, want 'not in this build yet', never an AT-01-only report", include, err)
		}
	}
	// Skipping every built check leaves nothing; unbuilt checks never fill the gap. The message below
	// keeps its Phase 1 text because hack/floor-guard.py flags any reworded assertion (D71).
	var built []string
	for _, s := range Registry {
		if s.Run != nil {
			built = append(built, s.ID)
		}
	}
	if _, err := Select(nil, built); !errors.Is(err, ErrNoChecks) {
		t.Errorf("Select(skip AT-01) error = %v, want ErrNoChecks (nothing else is built)", err)
	}
	specs, err := Select([]string{"AT-01"}, nil)
	if err != nil || len(specs) != 1 || specs[0].ID != "AT-01" || specs[0].Run == nil {
		t.Errorf("Select() = %v, %v; want only the built check AT-01", ResultIDs(specs), err)
	}
	wantBuilt := []string{"AT-01", "AT-02", "AT-03", "AT-04.1", "AT-04.2", "AT-04.3", "AT-04.4", "AT-04.5", "AT-05"} // grows as checks land
	specs, err = Select(nil, nil)
	if err != nil || !slices.Equal(ResultIDs(specs), wantBuilt) || slices.ContainsFunc(specs, func(s Spec) bool { return s.Run == nil }) {
		t.Errorf("Select() = %v, %v; want every built check, %v, and nothing else", ResultIDs(specs), err, wantBuilt)
	}
}

func TestSelectRejectsUnknownIDs(t *testing.T) {
	implemented(t)
	for _, tt := range []struct{ include, skip []string }{
		{[]string{"AT-99"}, nil},
		{nil, []string{"at-01"}},
	} {
		_, err := Select(tt.include, tt.skip)
		id := slices.Concat(tt.include, tt.skip)[0]
		if err == nil || !strings.Contains(err.Error(), id) {
			t.Errorf("Select(%v, %v) error = %v, want one naming %q", tt.include, tt.skip, err, id)
		}
	}
}

func TestSelectRejectsAnEmptySet(t *testing.T) {
	implemented(t)
	_, err := Select([]string{"AT-04"}, []string{"AT-04"})
	if !errors.Is(err, ErrNoChecks) {
		t.Errorf("Select() error = %v, want ErrNoChecks", err)
	}
}
