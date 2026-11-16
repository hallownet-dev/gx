package main

import (
	"strings"
	"testing"
)

func TestAskYes(t *testing.T) {
	cases := map[string]bool{"\n": true, "y\n": true, "Y\n": true, "yes\n": true, "n\n": false, "": false, "nope\n": false}
	for in, want := range cases {
		if got := askYes(strings.NewReader(in), "Sign in?"); got != want {
			t.Errorf("askYes(%q) = %v, want %v", in, got, want)
		}
	}
}
