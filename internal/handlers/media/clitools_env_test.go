package media

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseExports_ExtractsAssignments(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, ".bashrc")
	body := `# comment with export FAKE=nope
export HERMES_BASE_URL="https://router.diama.dev/v1"
export OPENAI_BASE_URL=http://127.0.0.1:20128/v1   # trailing comment
PATH_EXTRA=/opt/bin
export QUOTED='single value'
`
	if err := os.WriteFile(rc, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	parseExports(rc, got)

	want := map[string]string{
		"HERMES_BASE_URL": "https://router.diama.dev/v1",
		"OPENAI_BASE_URL": "http://127.0.0.1:20128/v1",
		"PATH_EXTRA":      "/opt/bin",
		"QUOTED":          "single value",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["FAKE"]; ok {
		t.Error("commented-out export must not be parsed")
	}
}

// Compound assignments are deliberately out of scope: `if ...; then export
// X=1; fi` puts the export mid-line, which the parser treats as a non-identifier
// and skips. Documented ceiling — a card that needs this must gain real shell
// evaluation, not more string slicing.
func TestParseExports_SkipsCompoundCommand(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, ".bashrc")
	if err := os.WriteFile(rc, []byte("if [ -d /x ]; then export NOT_A_PAIR=1; fi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	parseExports(rc, got)
	if _, ok := got["NOT_A_PAIR"]; ok {
		t.Error("compound-command export is out of scope and must be skipped")
	}
}

func TestParseExports_MissingFileIsNoOp(t *testing.T) {
	got := map[string]string{}
	parseExports(filepath.Join(t.TempDir(), "nope"), got)
	if len(got) != 0 {
		t.Errorf("missing rc file must yield no entries, got %v", got)
	}
}

func TestPointsAtGateway(t *testing.T) {
	cases := map[string]bool{
		"https://router.diama.dev/v1":  true,
		"http://127.0.0.1:20128/v1":    true,
		"http://localhost:20128":       true,
		"https://router.diama.dev/v1/": true,
		"router.diama.dev/v1":          true, // no scheme
		"https://api.openai.com/v1":    false,
		"https://api.anthropic.com":    false,
		"":                             false,
		"   ":                          false,
		// Lookalike hosts must not read as ours.
		"https://evil-router.diama.dev.attacker.io/v1": false,
		"https://router.diama.dev.evil.io/v1":           false,
		"https://router.diama.dev@evil.io/v1":           false,
	}
	for in, want := range cases {
		if got := pointsAtGateway(in); got != want {
			t.Errorf("pointsAtGateway(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestHas9Router_RequiresInstallAndEnvKey(t *testing.T) {
	env := map[string]string{"HERMES_BASE_URL": "https://router.diama.dev/v1"}
	d := cliDetectors(env)

	// Hermes has both a binary probe and an env key, so the flag follows the
	// scanned value.
	hermes, _ := d["hermes"](t.Context())
	if hermes == nil {
		t.Fatal("hermes detector returned nil")
	}

	// A tool with no env key can never claim to be connected.
	cline, _ := d["cline"](t.Context())
	if cline != nil && cline.Installed && cline.Has9Router {
		t.Error("tool without envKey must never report Has9Router")
	}
}

func TestHas9Router_FalseWhenEnvPointsElsewhere(t *testing.T) {
	env := map[string]string{"HERMES_BASE_URL": "https://api.openai.com/v1"}
	d := cliDetectors(env)
	if s, _ := d["hermes"](t.Context()); s != nil && s.Has9Router {
		t.Error("Has9Router must be false when the env var points at a vendor")
	}
}
