package checks

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestSelect(t *testing.T) {
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

func TestSelectRejectsUnknownIDs(t *testing.T) {
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
	_, err := Select([]string{"AT-04"}, []string{"AT-04"})
	if !errors.Is(err, ErrNoChecks) {
		t.Errorf("Select() error = %v, want ErrNoChecks", err)
	}
}
