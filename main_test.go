package main

import (
	"flag"
	"testing"
)

var servers = []string{"auto", "01 🇩🇪GERM-1-REALITY", "13 🇩🇪GERM-1-WS", "15 🇫🇮FNK-1-REALITY"}

func TestResolve(t *testing.T) {
	cases := map[string]string{
		"13":       "13 🇩🇪GERM-1-WS",
		"1":        "01 🇩🇪GERM-1-REALITY",
		"auto":     "auto",
		"fnk":      "15 🇫🇮FNK-1-REALITY",
		"germ-1-w": "13 🇩🇪GERM-1-WS",
	}
	for q, want := range cases {
		got, err := resolve(servers, q)
		if err != nil || got != want {
			t.Errorf("resolve(%q) = %q, %v; want %q", q, got, err, want)
		}
	}
	if _, err := resolve(servers, "germ"); err == nil {
		t.Error("ambiguous query should fail")
	}
	if _, err := resolve(servers, "99"); err == nil {
		t.Error("missing server should fail")
	}
}

func TestParseInterleaved(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	sys := fs.Bool("system", false, "")
	ver := fs.String("core-version", "", "")
	pos := parse(fs, []string{"https://x/sub", "--system", "--core-version", "1.12.0"})
	if !*sys || *ver != "1.12.0" || len(pos) != 1 || pos[0] != "https://x/sub" {
		t.Fatalf("sys=%v ver=%q pos=%v", *sys, *ver, pos)
	}
}
