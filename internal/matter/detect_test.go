package matter

import (
	"errors"
	"testing"
)

func TestParseNodeMajor(t *testing.T) {
	cases := map[string]int{
		"v22.23.1": 22,
		"22.23.1":  22,
		"v18.0.0":  18,
		" v20.5.0": 20,
		"garbage":  0,
		"":         0,
	}
	for in, want := range cases {
		if got := parseNodeMajor(in); got != want {
			t.Errorf("parseNodeMajor(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestDetectNode(t *testing.T) {
	origLook, origRun := lookPath, runVersion
	defer func() { lookPath, runVersion = origLook, origRun }()

	t.Run("not found", func(t *testing.T) {
		lookPath = func(string) (string, error) { return "", errors.New("not found") }
		info := detectNode()
		if info.Available {
			t.Fatal("expected unavailable when node missing")
		}
		if info.Reason == "" {
			t.Error("expected a reason")
		}
	})

	t.Run("too old", func(t *testing.T) {
		lookPath = func(string) (string, error) { return "/usr/bin/node", nil }
		runVersion = func(string) (string, error) { return "v16.0.0\n", nil }
		info := detectNode()
		if info.Available {
			t.Fatal("expected unavailable for old node")
		}
		if info.Major != 16 {
			t.Errorf("major = %d, want 16", info.Major)
		}
	})

	t.Run("ok", func(t *testing.T) {
		lookPath = func(string) (string, error) { return "/usr/bin/node", nil }
		runVersion = func(string) (string, error) { return "v20.11.0\n", nil }
		info := detectNode()
		if !info.Available {
			t.Fatalf("expected available, reason=%q", info.Reason)
		}
		if info.Major != 20 {
			t.Errorf("major = %d, want 20", info.Major)
		}
	})
}
