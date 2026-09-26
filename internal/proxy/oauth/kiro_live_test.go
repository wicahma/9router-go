package oauth

import (
	"context"
	json "encoding/json/v2"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestKiroLiveRefresh hits the real AWS SSO OIDC endpoint with the credential
// blob at /tmp/kiro_conn.json (written by ops tooling, never committed). It is
// skipped when the file is absent so CI stays hermetic.
func TestKiroLiveRefresh(t *testing.T) {
	raw, err := os.ReadFile("/tmp/kiro_conn.json")
	if err != nil {
		t.Skip("no live kiro connection blob")
	}
	var conn struct {
		RefreshToken         string            `json:"refreshToken"`
		AccessToken          string            `json:"accessToken"`
		ProviderSpecificData map[string]string `json:"providerSpecificData"`
	}
	if err := json.Unmarshal(raw, &conn); err != nil {
		t.Fatalf("decode blob: %v", err)
	}

	psd := map[string]string{}
	for k, v := range conn.ProviderSpecificData {
		if v != "" {
			psd[k] = v
		}
	}
	t.Logf("psd keys: %s", strings.Join(keysOf(psd), ","))

	res, err := Refresh(context.Background(), &Params{
		Client:               &http.Client{Timeout: 30 * time.Second},
		Provider:             "kiro",
		RefreshToken:         conn.RefreshToken,
		AccessToken:          conn.AccessToken,
		ProviderSpecificData: psd,
	})
	if err != nil {
		t.Fatalf("live refresh failed: %v", err)
	}
	if res.AccessToken == "" {
		t.Fatal("no access token returned")
	}
	t.Logf("OK expiresIn=%d rotated=%v", res.ExpiresIn, res.AccessToken != conn.AccessToken)
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
