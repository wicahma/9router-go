package retention

import (
	"reflect"
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestLoadConfigDefaults(t *testing.T) {
	v := viper.New()
	cfg := LoadConfig(v)

	if !cfg.Enabled {
		t.Errorf("expected Enabled=true, got %v", cfg.Enabled)
	}
	if cfg.Interval != 15*time.Minute {
		t.Errorf("expected Interval=15m, got %v", cfg.Interval)
	}
	if cfg.DBMaxBytes != 256*1024*1024 {
		t.Errorf("expected DBMaxBytes=268435456, got %d", cfg.DBMaxBytes)
	}
	if cfg.UsageHistoryMaxAge != 720*time.Hour {
		t.Errorf("expected UsageHistoryMaxAge=720h, got %v", cfg.UsageHistoryMaxAge)
	}
	if cfg.RequestDetailsMaxAge != 72*time.Hour {
		t.Errorf("expected RequestDetailsMaxAge=72h, got %v", cfg.RequestDetailsMaxAge)
	}
	if cfg.RequestDetailsMaxRows != 20000 {
		t.Errorf("expected RequestDetailsMaxRows=20000, got %d", cfg.RequestDetailsMaxRows)
	}
	if cfg.LogMaxBytes != 16*1024*1024 {
		t.Errorf("expected LogMaxBytes=16777216, got %d", cfg.LogMaxBytes)
	}
	expectedLogFiles := []string{"/var/log/9router-go.log", "/var/log/9router-go-error.log"}
	if !reflect.DeepEqual(cfg.LogFiles, expectedLogFiles) {
		t.Errorf("expected LogFiles=%v, got %v", expectedLogFiles, cfg.LogFiles)
	}
}

func TestLoadConfigCustomValues(t *testing.T) {
	v := viper.New()
	v.Set("RETENTION_ENABLED", false)
	v.Set("RETENTION_INTERVAL", "30m")
	v.Set("RETENTION_DB_MAX_MB", 512)
	v.Set("RETENTION_USAGE_HISTORY_MAX_AGE_HOURS", 100)
	v.Set("RETENTION_REQUEST_DETAILS_MAX_AGE_HOURS", 48)
	v.Set("RETENTION_REQUEST_DETAILS_MAX_ROWS", 5000)
	v.Set("RETENTION_LOG_MAX_MB", 32)
	v.Set("RETENTION_LOG_FILES", "/tmp/a.log,/tmp/b.log")

	cfg := LoadConfig(v)

	if cfg.Enabled != false {
		t.Errorf("expected Enabled=false, got %v", cfg.Enabled)
	}
	if cfg.Interval != 30*time.Minute {
		t.Errorf("expected Interval=30m, got %v", cfg.Interval)
	}
	if cfg.DBMaxBytes != 512*1024*1024 {
		t.Errorf("expected DBMaxBytes=536870912, got %d", cfg.DBMaxBytes)
	}
	if cfg.UsageHistoryMaxAge != 100*time.Hour {
		t.Errorf("expected UsageHistoryMaxAge=100h, got %v", cfg.UsageHistoryMaxAge)
	}
	if cfg.RequestDetailsMaxAge != 48*time.Hour {
		t.Errorf("expected RequestDetailsMaxAge=48h, got %v", cfg.RequestDetailsMaxAge)
	}
	if cfg.RequestDetailsMaxRows != 5000 {
		t.Errorf("expected RequestDetailsMaxRows=5000, got %d", cfg.RequestDetailsMaxRows)
	}
	if cfg.LogMaxBytes != 32*1024*1024 {
		t.Errorf("expected LogMaxBytes=33554432, got %d", cfg.LogMaxBytes)
	}
	expectedLogFiles := []string{"/tmp/a.log", "/tmp/b.log"}
	if !reflect.DeepEqual(cfg.LogFiles, expectedLogFiles) {
		t.Errorf("expected LogFiles=%v, got %v", expectedLogFiles, cfg.LogFiles)
	}
}

func TestLoadConfigDisabledLimits(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*viper.Viper)
	}{
		{
			name: "zero values",
			setup: func(v *viper.Viper) {
				v.Set("RETENTION_DB_MAX_MB", 0)
				v.Set("RETENTION_USAGE_HISTORY_MAX_AGE_HOURS", 0)
				v.Set("RETENTION_REQUEST_DETAILS_MAX_AGE_HOURS", 0)
				v.Set("RETENTION_REQUEST_DETAILS_MAX_ROWS", 0)
				v.Set("RETENTION_LOG_MAX_MB", 0)
			},
		},
		{
			name: "negative values",
			setup: func(v *viper.Viper) {
				v.Set("RETENTION_DB_MAX_MB", -10)
				v.Set("RETENTION_USAGE_HISTORY_MAX_AGE_HOURS", -1)
				v.Set("RETENTION_REQUEST_DETAILS_MAX_AGE_HOURS", -5)
				v.Set("RETENTION_REQUEST_DETAILS_MAX_ROWS", -100)
				v.Set("RETENTION_LOG_MAX_MB", -16)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := viper.New()
			tc.setup(v)
			cfg := LoadConfig(v)

			if cfg.DBMaxBytes != 0 {
				t.Errorf("expected DBMaxBytes=0, got %d", cfg.DBMaxBytes)
			}
			if cfg.UsageHistoryMaxAge != 0 {
				t.Errorf("expected UsageHistoryMaxAge=0, got %v", cfg.UsageHistoryMaxAge)
			}
			if cfg.RequestDetailsMaxAge != 0 {
				t.Errorf("expected RequestDetailsMaxAge=0, got %v", cfg.RequestDetailsMaxAge)
			}
			if cfg.RequestDetailsMaxRows != 0 {
				t.Errorf("expected RequestDetailsMaxRows=0, got %d", cfg.RequestDetailsMaxRows)
			}
			if cfg.LogMaxBytes != 0 {
				t.Errorf("expected LogMaxBytes=0, got %d", cfg.LogMaxBytes)
			}
		})
	}
}

func TestLoadConfigNilViper(t *testing.T) {
	cfg := LoadConfig(nil)
	if cfg == nil {
		t.Fatal("expected non-nil config when viper is nil")
	}
	if !cfg.Enabled {
		t.Errorf("expected Enabled=true, got %v", cfg.Enabled)
	}
	if cfg.Interval != 15*time.Minute {
		t.Errorf("expected Interval=15m, got %v", cfg.Interval)
	}
}
