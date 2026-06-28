package xiaov2board

import (
	"reflect"
	"testing"
)

func TestParseSourceIDs(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []int64
	}{
		{name: "json number array", raw: `[1,2,3]`, want: []int64{1, 2, 3}},
		{name: "json string array", raw: `["1","2"]`, want: []int64{1, 2}},
		{name: "single number", raw: `3`, want: []int64{3}},
		{name: "csv", raw: `1,2,2`, want: []int64{1, 2}},
		{name: "empty", raw: ``, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSourceIDs(tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseSourceIDs(%q) = %#v, want %#v", tt.raw, got, tt.want)
			}
		})
	}
}
