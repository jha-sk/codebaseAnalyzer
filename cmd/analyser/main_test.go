package main

import "testing"

func TestNewRootCmd(t *testing.T) {
	cmd := newRootCmd()
	if cmd.Use != "analyser" {
		t.Errorf("Use = %q, want %q", cmd.Use, "analyser")
	}
}
