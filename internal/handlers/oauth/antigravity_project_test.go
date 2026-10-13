package oauth

import (
	"context"
	"net/http"
	"testing"
)

func TestSetAntigravityProjectID(t *testing.T) {
	tests := []struct {
		name          string
		data          map[string]any
		pid           string
		wantTopLevel  string
		wantPSD       any
		wantPreserved any
	}{
		{
			name:         "creates providerSpecificData when absent",
			data:         map[string]any{"apiKey": "tok"},
			pid:          "proj-1",
			wantTopLevel: "proj-1",
			wantPSD:      "proj-1",
		},
		{
			name:          "preserves existing providerSpecificData keys",
			data:          map[string]any{"providerSpecificData": map[string]any{"baseUrl": "https://example.test"}},
			pid:           "proj-2",
			wantTopLevel:  "proj-2",
			wantPSD:       "proj-2",
			wantPreserved: "https://example.test",
		},
		{
			name:         "empty project id writes nothing",
			data:         map[string]any{"apiKey": "tok"},
			pid:          "",
			wantTopLevel: "",
			wantPSD:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setAntigravityProjectID(tt.data, tt.pid)

			got, _ := tt.data["projectId"].(string)
			if got != tt.wantTopLevel {
				t.Errorf("top-level projectId = %q, want %q", got, tt.wantTopLevel)
			}

			psd, _ := tt.data["providerSpecificData"].(map[string]any)
			var gotPSD any
			if psd != nil {
				gotPSD = psd["projectId"]
			}
			if gotPSD != tt.wantPSD {
				t.Errorf("providerSpecificData.projectId = %v, want %v", gotPSD, tt.wantPSD)
			}

			if tt.wantPreserved != nil {
				if psd == nil || psd["baseUrl"] != tt.wantPreserved {
					t.Errorf("providerSpecificData.baseUrl = %v, want %v", psd["baseUrl"], tt.wantPreserved)
				}
			}
		})
	}
}

func TestAntigravityProjectIDForToken(t *testing.T) {
	tests := []struct {
		name        string
		accessToken string
		stubPID     string
		want        string
		wantCalled  bool
	}{
		{
			name:        "blank access token skips probe",
			accessToken: "",
			stubPID:     "ignored",
			want:        "",
			wantCalled:  false,
		},
		{
			name:        "probe result is returned",
			accessToken: "token-abc",
			stubPID:     "my-proj",
			want:        "my-proj",
			wantCalled:  true,
		},
		{
			name:        "probe without project returns empty",
			accessToken: "token-abc",
			stubPID:     "",
			want:        "",
			wantCalled:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := fetchProjectIDFn
			t.Cleanup(func() { fetchProjectIDFn = original })

			called := false
			fetchProjectIDFn = func(_ context.Context, _ *http.Client, _ string) (string, bool, bool, bool) {
				called = true
				return tt.stubPID, false, false, false
			}

			got := antigravityProjectIDForToken(context.Background(), tt.accessToken)
			if got != tt.want {
				t.Errorf("antigravityProjectIDForToken() = %q, want %q", got, tt.want)
			}
			if called != tt.wantCalled {
				t.Errorf("probe called = %v, want %v", called, tt.wantCalled)
			}
		})
	}
}
