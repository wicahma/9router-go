package chat

import (
	"testing"

	"9router/proxy/internal/providers"
)

func TestMergeConnectionHeaders(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *providers.ProviderConfig
		connData *ConnectionData
		want     map[string]string
	}{
		{
			name: "headers present and nil StaticHeaders",
			cfg:  &providers.ProviderConfig{},
			connData: &ConnectionData{ProviderSpecificData: map[string]any{
				"headers": map[string]any{"Origin": "https://example.com", "X-Custom": "1"},
			}},
			want: map[string]string{"Origin": "https://example.com", "X-Custom": "1"},
		},
		{
			name: "existing key not overwritten",
			cfg:  &providers.ProviderConfig{StaticHeaders: map[string]string{"User-Agent": "provider-ua"}},
			connData: &ConnectionData{ProviderSpecificData: map[string]any{
				"headers": map[string]any{"User-Agent": "spoofed", "X-Custom": "1"},
			}},
			want: map[string]string{"User-Agent": "provider-ua", "X-Custom": "1"},
		},
		{
			name: "non-string values coerced",
			cfg:  &providers.ProviderConfig{},
			connData: &ConnectionData{ProviderSpecificData: map[string]any{
				"headers": map[string]any{"X-Number": float64(42), "X-Bool": true},
			}},
			want: map[string]string{"X-Number": "42", "X-Bool": "true"},
		},
		{
			name:     "providerSpecificData missing",
			cfg:      &providers.ProviderConfig{},
			connData: &ConnectionData{},
			want:     nil,
		},
		{
			name: "providerSpecificData not a map",
			cfg:  &providers.ProviderConfig{},
			connData: &ConnectionData{ProviderSpecificData: map[string]any{
				"headers": "not-a-map",
			}},
			want: nil,
		},
		{
			name: "headers empty map",
			cfg:  &providers.ProviderConfig{},
			connData: &ConnectionData{ProviderSpecificData: map[string]any{
				"headers": map[string]any{},
			}},
			want: nil,
		},
		{
			name: "nil entry value skipped",
			cfg:  &providers.ProviderConfig{},
			connData: &ConnectionData{ProviderSpecificData: map[string]any{
				"headers": map[string]any{"X-Nil": nil, "X-Keep": "yes"},
			}},
			want: map[string]string{"X-Keep": "yes"},
		},
		{
			name:     "nil connData",
			cfg:      &providers.ProviderConfig{},
			connData: nil,
			want:     nil,
		},
		{
			name: "empty key skipped",
			cfg:  &providers.ProviderConfig{},
			connData: &ConnectionData{ProviderSpecificData: map[string]any{
				"headers": map[string]any{"": "value"},
			}},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mergeConnectionHeaders(tt.cfg, tt.connData)
			if tt.want == nil {
				if tt.cfg.StaticHeaders != nil {
					t.Fatalf("StaticHeaders = %v, want nil", tt.cfg.StaticHeaders)
				}
				return
			}
			if len(tt.cfg.StaticHeaders) != len(tt.want) {
				t.Fatalf("StaticHeaders = %v, want %v", tt.cfg.StaticHeaders, tt.want)
			}
			for k, v := range tt.want {
				if got := tt.cfg.StaticHeaders[k]; got != v {
					t.Errorf("StaticHeaders[%q] = %q, want %q", k, got, v)
				}
			}
		})
	}
}
