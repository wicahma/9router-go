package dashboard

import (
	"testing"

	"9router/proxy/internal/models"
)

func TestSanitizeProviderConnectionProjectID(t *testing.T) {
	conn := &models.ProviderConnection{
		ID:       "conn-sanitize-ag",
		Provider: "antigravity",
		AuthType: "oauth",
		Data: `{"apiKey":"sk-secret","accessToken":"ya29.secret","refreshToken":"1//secret",` +
			`"projectId":"proj-oauth-1","providerSpecificData":{"projectId":"proj-oauth-1","baseUrl":"https://example.test"}}`,
	}

	safe := sanitizeProviderConnection(conn)

	if safe["projectId"] != "proj-oauth-1" {
		t.Errorf("top-level projectId = %v, want proj-oauth-1", safe["projectId"])
	}

	psd, ok := safe["providerSpecificData"].(map[string]any)
	if !ok || psd["projectId"] != "proj-oauth-1" {
		t.Errorf("providerSpecificData.projectId = %v, want proj-oauth-1", safe["providerSpecificData"])
	}

	for _, secret := range []string{"apiKey", "accessToken", "refreshToken"} {
		if _, present := safe[secret]; present {
			t.Errorf("secret %q leaked into sanitized connection", secret)
		}
	}
}
