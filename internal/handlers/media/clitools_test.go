package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectAll_OneFailingDetectorIsNull(t *testing.T) {
	ctx := context.Background()
	v := "1.2.3"
	det := map[string]toolDetector{
		"ok": func(context.Context) (*cliStatus, error) {
			return &cliStatus{Installed: true, Version: &v}, nil
		},
		"bad": func(context.Context) (*cliStatus, error) {
			return nil, errors.New("boom")
		},
		"panic-tool": func(context.Context) (*cliStatus, error) {
			panic("probe threw")
		},
		"none": func(context.Context) (*cliStatus, error) {
			return &cliStatus{Installed: false}, nil
		},
	}

	res := detectAll(ctx, det)

	if res["bad"] != nil {
		t.Fatalf("failing detector should map to null, got %+v", res["bad"])
	}
	if res["panic-tool"] != nil {
		t.Fatalf("panicking detector should map to null, got %+v", res["panic-tool"])
	}
	if res["ok"] == nil || !res["ok"].Installed || res["ok"].Version == nil || *res["ok"].Version != v {
		t.Fatalf("ok tool mangled: %+v", res["ok"])
	}
	if res["none"] == nil || res["none"].Installed {
		t.Fatalf("not-installed tool mangled: %+v", res["none"])
	}
}

func TestLookupToolBin_FindsBinaryOutsidePATH(t *testing.T) {
	// The service user's PATH never contains the operator's ~/.local/bin, so
	// detection has to consult the extra install roots; a tool living only
	// there must still be reported installed.
	dir := t.TempDir()
	bin := filepath.Join(dir, "fakecli")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := userBinDirs
	userBinDirs = []string{dir}
	t.Cleanup(func() { userBinDirs = old })

	if p, ok := lookupToolBin("fakecli"); !ok || p != bin {
		t.Fatalf("expected %q to be found, got %q ok=%v", bin, p, ok)
	}
	if _, ok := lookupToolBin("definitely-not-installed-xyz"); ok {
		t.Fatal("expected miss for a binary that does not exist")
	}
}

func TestLookupToolBin_SkipsNonExecutable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fakecli"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := userBinDirs
	userBinDirs = []string{dir}
	t.Cleanup(func() { userBinDirs = old })

	if _, ok := lookupToolBin("fakecli"); ok {
		t.Fatal("non-executable file must not count as installed")
	}
}

func TestCLIDetectors_HasAllToolIDs(t *testing.T) {
	// The tool ID set is load-bearing: it must match what the dashboard's
	// status card renders. A missing or renamed ID silently drops a card.
	want := []string{
		"claude", "codex", "opencode", "droid", "openclaw", "hermes",
		"cowork", "copilot", "cline", "kilo", "deepseek-tui", "jcode",
		"grok-build", "devin",
	}
	m := cliDetectors()
	for _, id := range want {
		if _, ok := m[id]; !ok {
			t.Errorf("missing tool detector for %q", id)
		}
	}
	if len(m) != len(want) {
		t.Errorf("got %d detectors, want %d", len(m), len(want))
	}
}