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
	for _, id := range include {
		if !available(strings.TrimSpace(id)) {
			return nil, fmt.Errorf("check %s is not in this build yet; run list-checks", strings.TrimSpace(id))
		}
	}
	var selected []Spec
	for _, s := range Registry {
		if s.Run == nil {
			continue // shortcut: catalog-only until its task lands; at v0.1.0 drop unshipped checks from Registry instead (D28)
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

// available reports whether id names a check, or a result of a check, that has a Run function.
func available(id string) bool {
	for _, s := range Registry {
		if s.Run != nil && (s.ID == id || slices.ContainsFunc(s.Results, func(r ResultSpec) bool { return r.ID == id })) {
			return true
		}
	}
	return false
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
