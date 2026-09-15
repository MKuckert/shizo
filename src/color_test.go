package main

import (
	"bytes"
	"os"
	"testing"
)

func TestColorerWrap(t *testing.T) {
	tests := []struct {
		name string
		fn   func(colorer, string) string
		code string
	}{
		{"error", colorer.error, "31"},
		{"success", colorer.success, "32"},
		{"warning", colorer.warning, "33"},
	}
	for _, tt := range tests {
		t.Run(tt.name+"/enabled", func(t *testing.T) {
			got := tt.fn(colorer{enabled: true}, "x")
			want := "\033[" + tt.code + "mx\033[0m"
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
		t.Run(tt.name+"/disabled", func(t *testing.T) {
			got := tt.fn(colorer{enabled: false}, "x")
			if got != "x" {
				t.Errorf("got %q, want %q", got, "x")
			}
		})
	}
}

func TestNewColorerNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	c := newColorer(os.Stdout)
	if c.enabled {
		t.Error("NO_COLOR should disable color")
	}
}

func TestNewColorerNonFile(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	c := newColorer(&bytes.Buffer{})
	if c.enabled {
		t.Error("non-file writer should disable color")
	}
}
