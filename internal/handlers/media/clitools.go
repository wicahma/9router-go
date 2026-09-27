package media

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"9router/proxy/internal/handlerutil"
)

// cliStatus is the per-tool installed/version shape the dashboard expects
// (port of each Next <tool>-settings GET, trimmed to just install + version).
type cliStatus struct {
	Installed bool    `json:"installed"`
	Version   *string `json:"version"`
}

// cliVersionTimeout bounds each `--version` probe; install detection via
// LookPath is synchronous with no timeout.
const cliVersionTimeout = 2 * time.Second

// userBinDirs are per-user install roots a CLI is commonly unpacked into. The
// service runs as its own unprivileged user (9router) whose PATH is the bare
// system default, so a tool the dashboard's own user installed under ~/.local
// bin is invisible to exec.LookPath alone — every tool would report
// "not installed" even though it is on the operator's machine.
//
// Read access to these dirs is granted per-file via ACL; the OS permission model
// decides the rest, and a miss is just a miss. ponytail: no recursive walk, no
// $HOME discovery, no shelling out. Configurable per deployment if another
// operator's home lives elsewhere.
var userBinDirs = []string{
	"/home/diama/.local/bin",
	"/home/diama/bin",
	"/home/diama/.bun/bin",
	"/home/diama/.npm-global/bin",
	"/home/diama/.cargo/bin",
}

// toolDetector reports one tool's status, or err/panic → null (the reference
// all-statuses route wraps each tool GET in try/catch and maps throws to null).
type toolDetector func(context.Context) (*cliStatus, error)

// toolDef captures how each tool's presence is checked, mirroring the
// reference GET routes: mostly which <bin>; `devin` also reports version;
// `cowork` checks Claude Desktop config dirs; `copilot` has no binary check
// (reference hardcodes installed:true).
type toolDef struct {
	id        string
	bin       string
	hasVer    bool
	dirCheck  func() bool
	alwaysOn  bool
}

var cliTools = []toolDef{
	{id: "claude", bin: "claude"},
	{id: "codex", bin: "codex"},
	{id: "opencode", bin: "opencode"},
	{id: "droid", bin: "droid"},
	{id: "openclaw", bin: "openclaw"},
	{id: "hermes", bin: "hermes"},
	{id: "cowork", dirCheck: coworkDirOK},
	{id: "copilot", alwaysOn: true}, // VS Code config tool; reference returns installed:true
	{id: "cline", bin: "cline"},
	{id: "kilo", bin: "kilo"},
	{id: "deepseek-tui", bin: "deepseek"},
	{id: "jcode", bin: "jcode"},
	{id: "grok-build", bin: "grok"},
	{id: "devin", bin: "devin", hasVer: true},
}

// CLIToolsHandler aggregates per-tool CLI install/version status for the
// dashboard (port of the Next /cli-tools/all-statuses batch GET).
type CLIToolsHandler struct{}

// NewCLIToolsHandler returns a CLIToolsHandler. Stateless: nothing to wire up.
func NewCLIToolsHandler() *CLIToolsHandler {
	return &CLIToolsHandler{}
}

// HandleAllStatuses returns a flat {toolId: {installed, version}|null} map.
// A tool that errors or panics during detection reports null, matching the
// reference's per-tool try/catch.
func (h *CLIToolsHandler) HandleAllStatuses(w http.ResponseWriter, r *http.Request) {
	handlerutil.WriteJSON(w, http.StatusOK, cliStatuses(r.Context()))
}

// cliStatuses aggregates statuses for all known CLI tools.
func cliStatuses(ctx context.Context) map[string]*cliStatus {
	return detectAll(ctx, cliDetectors())
}

// cliDetectors builds the detection map for every known tool.
func cliDetectors() map[string]toolDetector {
	m := make(map[string]toolDetector, len(cliTools))
	for _, t := range cliTools {
		m[t.id] = t.detector()
	}
	return m
}

// detector returns the tool's status probe. Presence is checked on the user
// PATH via exec.LookPath (scope: plain PATH, no extra-bins trick).
func (t toolDef) detector() toolDetector {
	switch {
	case t.alwaysOn:
		return func(context.Context) (*cliStatus, error) {
			return &cliStatus{Installed: true}, nil
		}
	case t.dirCheck != nil:
		return func(context.Context) (*cliStatus, error) {
			return &cliStatus{Installed: t.dirCheck()}, nil
		}
	default:
		return func(ctx context.Context) (*cliStatus, error) {
			bin, ok := lookupToolBin(t.bin)
			if !ok {
				// Not installed is a value, not an error (tool ≠ null).
				return &cliStatus{Installed: false}, nil
			}
			s := &cliStatus{Installed: true}
			if t.hasVer {
				if v, err := binVersion(ctx, bin); err == nil && v != "" {
					s.Version = &v
				}
			}
			return s, nil
		}
	}
}

// lookupToolBin resolves a tool's binary against the process PATH first, then
// the per-user install roots. Returns the resolved path, which binVersion needs
// (a bare name would not resolve from the service's PATH).
func lookupToolBin(name string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	for _, dir := range userBinDirs {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode().Perm()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}

// detectAll runs each tool's detector under a per-tool try/catch; a detector
// that errors or panics maps to null (reference all-statuses behavior).
func detectAll(ctx context.Context, det map[string]toolDetector) map[string]*cliStatus {
	out := make(map[string]*cliStatus, len(det))
	for id, d := range det {
		out[id] = runDetector(ctx, d)
	}
	return out
}

// runDetector invokes a detector and converts err/panic to null.
func runDetector(ctx context.Context, d toolDetector) (s *cliStatus) {
	defer func() {
		if recover() != nil {
			s = nil
		}
	}()
	s, err := d(ctx)
	if err != nil {
		return nil
	}
	return s
}

// binVersion reads the binary's first-line `--version` output (matches the
// reference devin route).
func binVersion(ctx context.Context, bin string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, cliVersionTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "--version")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return "", err
	}
	line := strings.TrimSpace(buf.String())
	if line == "" {
		return "", nil
	}
	return strings.SplitN(line, "\n", 2)[0], nil
}

// coworkDirOK reports whether a Claude Desktop config dir exists (macOS),
// mirroring the reference cowork checkInstalled roots.
func coworkDirOK() bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	base := filepath.Join(home, "Library", "Application Support")
	for _, d := range []string{"Claude-3p", "Claude"} {
		if fi, err := os.Stat(filepath.Join(base, d)); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}