package providers

import (
	"testing"
	"time"
)

func TestNeedsRefresh(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		data      *OAuthConnectionData
		want      bool
		wantStale bool // expected IsExpired() value where asserted
		checkBoth bool
	}{
		{
			name: "nil receiver",
			data: nil,
			want: false,
		},
		{
			name: "empty ExpiresAt",
			data: &OAuthConnectionData{RefreshToken: "rt"},
			want: true,
		},
		{
			name: "garbage ExpiresAt",
			data: &OAuthConnectionData{RefreshToken: "rt", ExpiresAt: "not-a-time"},
			want: true,
		},
		{
			name: "expiry 1h future",
			data: &OAuthConnectionData{RefreshToken: "rt", ExpiresAt: now.Add(time.Hour).Format(time.RFC3339)},
			want: false,
		},
		{
			name:      "expiry 4m future inside window not expired",
			data:      &OAuthConnectionData{RefreshToken: "rt", ExpiresAt: now.Add(4 * time.Minute).Format(time.RFC3339)},
			want:      true,
			wantStale: false,
			checkBoth: true,
		},
		{
			name: "expiry 10m future",
			data: &OAuthConnectionData{RefreshToken: "rt", ExpiresAt: now.Add(10 * time.Minute).Format(time.RFC3339)},
			want: false,
		},
		{
			name:      "expiry 1m past",
			data:      &OAuthConnectionData{RefreshToken: "rt", ExpiresAt: now.Add(-time.Minute).Format(time.RFC3339)},
			want:      true,
			wantStale: true,
			checkBoth: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.data.NeedsRefresh(); got != tt.want {
				t.Errorf("NeedsRefresh() = %v, want %v", got, tt.want)
			}
			if tt.checkBoth {
				if got := tt.data.IsExpired(); got != tt.wantStale {
					t.Errorf("IsExpired() = %v, want %v", got, tt.wantStale)
				}
			}
		})
	}
}
