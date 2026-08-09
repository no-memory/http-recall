package main

import (
	"math"
	"testing"
)

func TestRateFlagValueSet(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantInfinity bool
		wantLimit    float64
		wantString   string
	}{
		{name: "infinity", input: "infinity", wantInfinity: true},
		{name: "zero means infinity", input: "0", wantInfinity: true},
		{name: "plain number is per second", input: "50", wantLimit: 50, wantString: "50"},
		{name: "per millisecond", input: "10/ms", wantLimit: 10000, wantString: "10/ms"},
		{name: "custom duration", input: "2/500ms", wantLimit: 4, wantString: "2/500ms"},
		{name: "duration unit shorthand", input: "3/m", wantLimit: 0.05, wantString: "3/m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f rateFlagValue
			if err := f.Set(tt.input); err != nil {
				t.Fatalf("Set(%q) returned error: %v", tt.input, err)
			}

			limit := f.Limit()
			if tt.wantInfinity {
				if limit != nil {
					t.Fatalf("Limit() = %v, want nil for infinity", *limit)
				}
				return
			}

			if limit == nil {
				t.Fatal("Limit() = nil, want finite limit")
			}
			if diff := math.Abs(float64(*limit) - tt.wantLimit); diff > 1e-9 {
				t.Fatalf("Limit() = %v, want %v", *limit, tt.wantLimit)
			}
			if f.String() != tt.wantString {
				t.Fatalf("String() = %q, want %q", f.String(), tt.wantString)
			}
		})
	}
}

func TestRateFlagValueSetRejectsInvalidInput(t *testing.T) {
	for _, input := range []string{"", "abc", "10/fortnight", "1/", "1/2"} {
		t.Run(input, func(t *testing.T) {
			var f rateFlagValue
			if err := f.Set(input); err == nil {
				t.Fatalf("Set(%q) succeeded, want error", input)
			}
		})
	}
}
