package media

import (
	"os"
	"path/filepath"
	"strings"
)

// userHomeDir is the home directory of the machine's operator — the one whose
// rc files describe the CLI configuration the dashboard reports on. It is
// spelled out rather than read from os.UserHomeDir, which inside the service
// returns the unprivileged service account's home, not the operator's.
const userHomeDir = "/home/diama"

// scanOperatorEnv reads the dashboard operator's shell startup files and
// returns every `export NAME=value` assignment it finds. Read-only: files are
// opened O_RDONLY and never written back. Runs once per request, so the reads do
// not scale with the 14 tools.
//
// ponytail: no shell eval, no $SHELL branching. A literal parse is all a status
// flag needs; add real evaluation only if a card must reflect a computed value
// like `export URL=$(cat /etc/router.url)`.
func scanOperatorEnv() map[string]string {
	home := operatorHome()
	if home == "" {
		return nil
	}
	out := make(map[string]string)
	for _, n := range []string{".bashrc", ".profile", ".bash_profile", ".zshrc"} {
		parseExports(filepath.Join(home, n), out)
	}
	return out
}

// operatorHome resolves the home directory whose rc files describe this machine's
// CLI configuration. The service user's own home is deliberately not used: it
// holds nothing the operator configured.
func operatorHome() string {
	if fi, err := os.Stat(userHomeDir); err == nil && fi.IsDir() {
		return userHomeDir
	}
	return ""
}

// parseExports appends assignments from one rc file into dst. Lines inside
// single-quoted heredocs are ignored only insofar as they fail the assignment
// pattern; a stray `FOO=bar` inside a heredoc would be picked up, which is an
// acceptable false positive for a status flag.
func parseExports(path string, dst map[string]string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		name, val, ok := parseExportLine(line)
		if ok {
			dst[name] = val
		}
	}
}

// parseExportLine pulls `NAME=value` out of a shell line, tolerating a leading
// `export`, surrounding quotes, and trailing `;` or `# comment`.
func parseExportLine(line string) (name, value string, ok bool) {
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, "export ")
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "#") {
		return "", "", false
	}
	name, rest, found := strings.Cut(s, "=")
	if !found {
		return "", "", false
	}
	// A bare word before `=` is not a valid identifier in any shell, so reject
	// things like `if x==1` or `foo bar=1` rather than inventing a key.
	name = strings.TrimSpace(name)
	if !isEnvName(name) {
		return "", "", false
	}
	value = strings.TrimSpace(rest)
	// Drop a trailing comment only when it is clearly outside the value.
	if idx := strings.Index(value, " #"); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	value = strings.Trim(value, `'"`)
	return name, value, true
}

// isEnvName reports whether s is a shell identifier: letter or underscore
// first, then letters/digits/underscores.
func isEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_':
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// pointsAtGateway reports whether a BASE_URL value routes to this gateway.
// Both loopback and the public hostname count, because an operator may set
// either; what matters is that the tool is aimed here rather than at a vendor.
//
// The host must MATCH, not merely contain: a prefix or suffix of the real
// hostname (evil-router.diama.dev.attacker.io, router.diama.dev.evil.io) is a
// different machine and must not read as "configured".
func pointsAtGateway(value string) bool {
	v := strings.TrimRight(strings.TrimSpace(value), "/")
	if v == "" {
		return false
	}
	host := hostOf(v)
	if host == "" {
		return false
	}
	host = strings.ToLower(host)
	for _, marker := range gatewayHosts {
		if host == marker {
			return true
		}
	}
	return false
}

// gatewayHosts are the exact hosts this gateway answers on. Comparison is
// equality on the parsed host so a lookalike domain cannot spoof it.
var gatewayHosts = []string{
	"127.0.0.1:20128",
	"localhost:20128",
	"router.diama.dev",
	"router.diama.dev:443",
	"router.diama.dev:80",
}

// hostOf extracts the authority from a URL-ish string, tolerating a missing
// scheme (`router.diama.dev/v1`) which is how these variables are often written.
func hostOf(v string) string {
	s := v
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if at := strings.LastIndex(s, "@"); at >= 0 {
		s = s[at+1:]
	}
	return s
}
