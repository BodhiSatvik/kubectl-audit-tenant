package checks

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrNoChecks is returned when --checks and --skip leave nothing to run.
var ErrNoChecks = errors.New("no checks selected")

// Select keeps the results named by include (all of them when include is empty), then drops
// those named by skip. An ID is a check (AT-04) or one result (AT-04.2). The returned specs
// keep registry order, carry only the selected results, and all have a Run function: asking
// for a check that is not in this build is an error, never a silent empty report.
func Select(include, skip []string) ([]Spec, error) {
	in, err := resolve(include)
	if err != nil {
		return nil, err
	}
	out, err := resolve(skip)
	if err != nil {
		return nil, err
	}
	var selected []Spec
	for _, s := range Registry {
		if s.Run == nil {
			if slices.ContainsFunc(s.Results, func(r ResultSpec) bool { return in[r.ID] }) {
				return nil, fmt.Errorf("check %s is not in this build yet; it lands in a later release", s.ID)
			}
			continue // catalog-only (D28); v0.1.0 ships every check, and the guard keeps its tests for a later one
		}
		var results []ResultSpec
		for _, r := range s.Results {
			if (len(include) == 0 || in[r.ID]) && !out[r.ID] {
				results = append(results, r)
			}
		}
		if len(results) > 0 {
			s.Results = results
			selected = append(selected, s)
		}
	}
	if len(selected) == 0 {
		return nil, ErrNoChecks
	}
	return selected, nil
}

// resolve expands check and result IDs into the set of result IDs they name.
func resolve(ids []string) (map[string]bool, error) {
	set := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id) // "AT-01, AT-05" splits with a leading space
		found := false
		for _, s := range Registry {
			for _, r := range s.Results {
				if id == s.ID || id == r.ID {
					set[r.ID] = true
					found = true
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown check ID %q; run list-checks for the valid IDs", id)
		}
	}
	return set, nil
}
