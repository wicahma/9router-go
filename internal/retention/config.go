package retention

import (
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Enabled               bool
	Interval              time.Duration
	DBMaxBytes            int64
	UsageHistoryMaxAge    time.Duration
	RequestDetailsMaxAge  time.Duration
	RequestDetailsMaxRows int
	LogMaxBytes           int64
	LogFiles              []string
}

func LoadConfig(v *viper.Viper) *Config {
	if v == nil {
		v = viper.New()
	}

	enabled := true
	if v.IsSet("RETENTION_ENABLED") {
		enabled = v.GetBool("RETENTION_ENABLED")
	}

	interval := 15 * time.Minute
	if v.IsSet("RETENTION_INTERVAL") {
		if d := v.GetDuration("RETENTION_INTERVAL"); d > 0 {
			interval = d
		}
	}

	dbMaxBytes := int64(256 * 1024 * 1024)
	if v.IsSet("RETENTION_DB_MAX_MB") {
		mb := v.GetInt("RETENTION_DB_MAX_MB")
		if mb <= 0 {
			dbMaxBytes = 0
		} else {
			dbMaxBytes = int64(mb) * 1024 * 1024
		}
	}

	usageMaxAge := time.Duration(720) * time.Hour
	if v.IsSet("RETENTION_USAGE_HISTORY_MAX_AGE_HOURS") {
		h := v.GetInt("RETENTION_USAGE_HISTORY_MAX_AGE_HOURS")
		if h <= 0 {
			usageMaxAge = 0
		} else {
			usageMaxAge = time.Duration(h) * time.Hour
		}
	}

	reqMaxAge := time.Duration(72) * time.Hour
	if v.IsSet("RETENTION_REQUEST_DETAILS_MAX_AGE_HOURS") {
		h := v.GetInt("RETENTION_REQUEST_DETAILS_MAX_AGE_HOURS")
		if h <= 0 {
			reqMaxAge = 0
		} else {
			reqMaxAge = time.Duration(h) * time.Hour
		}
	}

	reqMaxRows := 20000
	if v.IsSet("RETENTION_REQUEST_DETAILS_MAX_ROWS") {
		r := v.GetInt("RETENTION_REQUEST_DETAILS_MAX_ROWS")
		if r <= 0 {
			reqMaxRows = 0
		} else {
			reqMaxRows = r
		}
	}

	logMaxBytes := int64(16 * 1024 * 1024)
	if v.IsSet("RETENTION_LOG_MAX_MB") {
		mb := v.GetInt("RETENTION_LOG_MAX_MB")
		if mb <= 0 {
			logMaxBytes = 0
		} else {
			logMaxBytes = int64(mb) * 1024 * 1024
		}
	}

	logFiles := []string{"/var/log/9router-go.log", "/var/log/9router-go-error.log"}
	if v.IsSet("RETENTION_LOG_FILES") {
		rawFiles := v.GetStringSlice("RETENTION_LOG_FILES")
		var cleaned []string
		for _, f := range rawFiles {
			for _, part := range strings.Split(f, ",") {
				part = strings.TrimSpace(part)
				if part != "" {
					cleaned = append(cleaned, part)
				}
			}
		}
		logFiles = cleaned
	}

	return &Config{
		Enabled:               enabled,
		Interval:              interval,
		DBMaxBytes:            dbMaxBytes,
		UsageHistoryMaxAge:    usageMaxAge,
		RequestDetailsMaxAge:  reqMaxAge,
		RequestDetailsMaxRows: reqMaxRows,
		LogMaxBytes:           logMaxBytes,
		LogFiles:              logFiles,
	}
}
