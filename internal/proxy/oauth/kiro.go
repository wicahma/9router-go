package oauth

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

func init() {
	Register("kiro", refreshKiro)
}

// kiroSocialUA is the Kiro desktop client identity the social refresh endpoint expects.
var kiroSocialUA = "kiro-cli/1.0.0"

// kiroRegionPattern guards the region interpolated into the IDC token URL.
var kiroRegionPattern = regexp.MustCompile(`^[a-z]{2}-[a-z]+-[0-9]+$`)

// refreshKiro refreshes a Kiro token. AWS SSO OIDC (builder-id) accounts refresh
// against https://oidc.<region>.amazonaws.com/token using the per-account
// clientId/clientSecret captured at login (camelCase JSON contract); social
// tokens refresh via the desktop refresh endpoint.
func refreshKiro(ctx context.Context, p *Params) (*TokenResult, error) {
	if p.RefreshToken == "" {
		return nil, fmt.Errorf("kiro: refresh_token is required")
	}

	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	if psd := p.ProviderSpecificData; psd != nil {
		cid, cs := strings.TrimSpace(psd["clientId"]), strings.TrimSpace(psd["clientSecret"])
		if cid != "" && cs != "" {
			return refreshKiroAWS(ctx, client, p.RefreshToken, cid, cs, strings.TrimSpace(psd["region"]))
		}
	}
	return refreshKiroSocial(ctx, client, p.RefreshToken)
}

type kiroTokens struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int    `json:"expiresIn"`
}

// refreshKiroAWS refreshes an AWS SSO OIDC token via the per-account client
// credentials stored at login time.
func refreshKiroAWS(ctx context.Context, client *http.Client, refreshToken, clientID, clientSecret, region string) (*TokenResult, error) {
	if region == "" {
		region = "us-east-1"
	}
	if !kiroRegionPattern.MatchString(region) {
		return nil, fmt.Errorf("kiro: invalid region %q", region)
	}
	body, err := json.Marshal(map[string]string{
		"clientId":     clientID,
		"clientSecret": clientSecret,
		"refreshToken": refreshToken,
		"grantType":    "refresh_token",
	})
	if err != nil {
		return nil, fmt.Errorf("kiro: marshal refresh request: %w", err)
	}
	return kiroPost(ctx, client, "https://oidc."+region+".amazonaws.com/token", body, "", refreshToken)
}

// refreshKiroSocial refreshes a Kiro social token via the desktop refresh endpoint.
func refreshKiroSocial(ctx context.Context, client *http.Client, refreshToken string) (*TokenResult, error) {
	body, err := json.Marshal(map[string]string{"refreshToken": refreshToken})
	if err != nil {
		return nil, fmt.Errorf("kiro: marshal social refresh request: %w", err)
	}
	return kiroPost(ctx, client, "https://prod.us-east-1.auth.desktop.kiro.dev/refreshToken", body, kiroSocialUA, refreshToken)
}

// kiroPost POSTs a JSON refresh request and maps a 200 body to a TokenResult.
// expiresIn is passed through as-is (0 means "omitted"); the background loop
// substitutes a conservative window for that case.
func kiroPost(ctx context.Context, client *http.Client, url string, body []byte, ua, refreshToken string) (*TokenResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("kiro: create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kiro: refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("kiro: read refresh response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kiro: refresh returned status %d: %s", resp.StatusCode, truncateBody(respBody))
	}

	var tokens kiroTokens
	if err := json.Unmarshal(respBody, &tokens); err != nil {
		return nil, fmt.Errorf("kiro: parse refresh response: %w", err)
	}
	if tokens.AccessToken == "" {
		return nil, fmt.Errorf("kiro: empty access token in refresh response")
	}
	if tokens.RefreshToken == "" {
		tokens.RefreshToken = refreshToken
	}

	return &TokenResult{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ExpiresIn:    tokens.ExpiresIn,
	}, nil
}
