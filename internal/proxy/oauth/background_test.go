package oauth

import (
	"context"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/models"
)

func conn(id, provider, authType, data string) *models.ProviderConnection {
	return &models.ProviderConnection{ID: id, Provider: provider, AuthType: authType, Data: data}
}

func TestSelectConnectionsNeedingRefresh(t *testing.T) {
	now := time.Date(2026, 9, 26, 15, 0, 0, 0, time.UTC)
	soon := now.Add(10 * time.Minute).Format(time.RFC3339)
	far := now.Add(4 * time.Hour).Format(time.RFC3339)

	tests := []struct {
		name string
		conn *models.ProviderConnection
		want bool
	}{
		{
			name: "oauth token inside lead window selected",
			conn: conn("a", "kiro", "oauth", `{"accessToken":"x","refreshToken":"r","expiresAt":"`+soon+`"}`),
			want: true,
		},
		{
			name: "oauth token outside lead window skipped",
			conn: conn("b", "kiro", "oauth", `{"accessToken":"x","refreshToken":"r","expiresAt":"`+far+`"}`),
			want: false,
		},
		{
			name: "missing expiresAt skipped",
			conn: conn("c", "kiro", "oauth", `{"accessToken":"x","refreshToken":"r"}`),
			want: false,
		},
		{
			name: "unparsable expiresAt skipped",
			conn: conn("d", "kiro", "oauth", `{"refreshToken":"r","expiresAt":"not-a-time"}`),
			want: false,
		},
		{
			name: "no refresh token skipped",
			conn: conn("e", "kiro", "oauth", `{"accessToken":"x","expiresAt":"`+soon+`"}`),
			want: false,
		},
		{
			name: "api-key connection skipped",
			conn: conn("f", "kiro", "apikey", `{"apiKey":"k","refreshToken":"r","expiresAt":"`+soon+`"}`),
			want: false,
		},
		{
			name: "oauth_ underscore authType accepted",
			conn: conn("g", "kiro", "oauth_", `{"refreshToken":"r","expiresAt":"`+soon+`"}`),
			want: true,
		},
		{
			name: "empty data skipped",
			conn: conn("h", "kiro", "oauth", ""),
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SelectConnectionsNeedingRefresh([]*models.ProviderConnection{tc.conn}, now)
			if (len(got) == 1) != tc.want {
				t.Fatalf("selected=%d want=%v", len(got), tc.want)
			}
		})
	}

	if got := SelectConnectionsNeedingRefresh([]*models.ProviderConnection{nil}, now); len(got) != 0 {
		t.Fatalf("nil connection selected: %d", len(got))
	}
}

func TestSelectConnectionsCarriesProviderSpecificData(t *testing.T) {
	now := time.Now()
	data := `{"refreshToken":"r","expiresAt":"` + now.Add(time.Minute).Format(time.RFC3339) +
		`","providerSpecificData":{"clientId":"cid","clientSecret":"sec","region":"us-east-1","profileArn":null}}`

	got := SelectConnectionsNeedingRefresh([]*models.ProviderConnection{conn("k", "kiro", "oauth", data)}, now)
	if len(got) != 1 {
		t.Fatalf("selected=%d want=1", len(got))
	}
	psd := got[0].ProviderSpecificData
	if psd["clientId"] != "cid" || psd["clientSecret"] != "sec" || psd["region"] != "us-east-1" {
		t.Fatalf("providerSpecificData not flattened: %v", psd)
	}
	if _, present := psd["profileArn"]; present {
		t.Fatalf("null profileArn must be dropped: %v", psd)
	}
}

func TestStringMapDropsEmptyAndNonString(t *testing.T) {
	got := StringMap(map[string]any{"a": "x", "b": "", "c": 1, "d": nil})
	if len(got) != 1 || got["a"] != "x" {
		t.Fatalf("got %v", got)
	}
	if StringMap(nil) != nil || StringMap(map[string]any{}) != nil {
		t.Fatal("empty input must yield nil")
	}
}

// TestRefreshKiroRoutesByProviderSpecificData pins the AWS SSO OIDC branch: a
// connection carrying clientId/clientSecret must not fall through to the social
// endpoint. The region guard makes the branch observable offline.
func TestRefreshKiroRoutesByProviderSpecificData(t *testing.T) {
	_, err := refreshKiro(context.Background(), &Params{
		Provider:             "kiro",
		RefreshToken:         "rt",
		ProviderSpecificData: map[string]string{"clientId": "cid", "clientSecret": "sec", "region": "not-a-region"},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid region") {
		t.Fatalf("want invalid region error, got %v", err)
	}

	if _, err := refreshKiro(context.Background(), &Params{Provider: "kiro"}); err == nil {
		t.Fatal("missing refresh token must error")
	}
}

func TestRefreshKiroRegistered(t *testing.T) {
	if Get("kiro") == nil {
		t.Fatal("kiro refresher not registered")
	}
}
