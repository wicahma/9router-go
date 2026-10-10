package retention

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"9router/proxy/internal/db"
	"9router/proxy/internal/log"
)

func Run(ctx context.Context, repo *db.Repo, cfg *Config) error {
	if cfg == nil || !cfg.Enabled {
		return nil
	}

	log.Info("retention", "run start")

	var errs []error
	reqRows, err := pruneRequestDetails(ctx, repo, cfg)
	if err != nil {
		errs = append(errs, err)
	}

	usageRows, err := pruneUsageHistory(ctx, repo, cfg)
	if err != nil {
		errs = append(errs, err)
	}

	bytesFreed, err := reclaimSpace(ctx, repo, cfg)
	if err != nil {
		errs = append(errs, err)
	}

	if err := rotateLogs(ctx, cfg); err != nil {
		errs = append(errs, err)
	}

	totalRows := reqRows + usageRows
	log.Info("retention", "run finish", "pruned_rows", totalRows, "bytes_freed", bytesFreed)

	return errors.Join(errs...)
}

func StartBackground(ctx context.Context, repo *db.Repo, cfg *Config) {
	if cfg == nil || !cfg.Enabled {
		return
	}

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Minute):
		}

		safeRun(ctx, repo, cfg)

		interval := cfg.Interval
		if interval <= 0 {
			interval = 15 * time.Minute
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				safeRun(ctx, repo, cfg)
			}
		}
	}()
}

func safeRun(ctx context.Context, repo *db.Repo, cfg *Config) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("retention", "panic recovered in retention run", "panic", r)
		}
	}()
	if err := Run(ctx, repo, cfg); err != nil {
		log.Warn("retention", "retention pass finished with error", "error", err)
	}
}

func pruneRequestDetails(ctx context.Context, repo *db.Repo, cfg *Config) (int64, error) {
	if repo == nil {
		return 0, nil
	}
	var total int64
	var errs []error

	if cfg.RequestDetailsMaxAge > 0 {
		cutoff := time.Now().UTC().Add(-cfg.RequestDetailsMaxAge).Format("2006-01-02T15:04:05.000Z")
		n, err := repo.PruneRequestDetailsBefore(cutoff)
		if err != nil {
			errs = append(errs, err)
		} else {
			total += n
		}
	}

	if cfg.RequestDetailsMaxRows > 0 {
		n, err := repo.PruneRequestDetailsKeepNewest(cfg.RequestDetailsMaxRows)
		if err != nil {
			errs = append(errs, err)
		} else {
			total += n
		}
	}

	if total > 0 {
		log.Info("retention", "pruned requestDetails", "deleted_rows", total)
	}
	return total, errors.Join(errs...)
}

func pruneUsageHistory(ctx context.Context, repo *db.Repo, cfg *Config) (int64, error) {
	if repo == nil || cfg.UsageHistoryMaxAge <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-cfg.UsageHistoryMaxAge).Format(time.RFC3339)
	n, err := repo.PruneUsageHistoryBefore(cutoff)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		log.Info("retention", "pruned usageHistory", "deleted_rows", n)
	}
	return n, nil
}

func reclaimSpace(ctx context.Context, repo *db.Repo, cfg *Config) (int64, error) {
	if repo == nil {
		return 0, nil
	}
	rawDB := repo.RawDB()
	if rawDB == nil {
		return 0, nil
	}

	var errs []error
	var autoVacuum int
	if err := rawDB.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&autoVacuum); err != nil {
		errs = append(errs, fmt.Errorf("read auto_vacuum pragma: %w", err))
	} else if autoVacuum == 0 {
		if err := migrateAutoVacuum(ctx, rawDB); err != nil {
			errs = append(errs, err)
		}
	}

	var initialPages, pageSize int64
	_ = rawDB.QueryRowContext(ctx, "PRAGMA page_count").Scan(&initialPages)
	_ = rawDB.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize)
	initialBytes := initialPages * pageSize

	if cfg.DBMaxBytes <= 0 {
		_, _ = rawDB.ExecContext(ctx, "PRAGMA incremental_vacuum(1000)")
		var finalPages int64
		_ = rawDB.QueryRowContext(ctx, "PRAGMA page_count").Scan(&finalPages)
		finalBytes := finalPages * pageSize
		freed := initialBytes - finalBytes
		if freed < 0 {
			freed = 0
		}
		if freed > 0 {
			log.Info("retention", "reclaim space", "before_bytes", initialBytes, "after_bytes", finalBytes, "freed_bytes", freed)
		}
		return freed, errors.Join(errs...)
	}

	currentPages := initialPages
	currentBytes := currentPages * pageSize

	for i := 0; i < 200 && currentBytes > cfg.DBMaxBytes; i++ {
		_, err := rawDB.ExecContext(ctx, "PRAGMA incremental_vacuum(1000)")
		if err != nil {
			errs = append(errs, fmt.Errorf("incremental_vacuum pass %d: %w", i, err))
			break
		}
		var newPages int64
		if err := rawDB.QueryRowContext(ctx, "PRAGMA page_count").Scan(&newPages); err != nil {
			break
		}
		if newPages >= currentPages {
			break
		}
		currentPages = newPages
		currentBytes = currentPages * pageSize
	}

	finalBytes := currentPages * pageSize
	freed := initialBytes - finalBytes
	if freed < 0 {
		freed = 0
	}
	log.Info("retention", "reclaim space", "before_bytes", initialBytes, "after_bytes", finalBytes, "freed_bytes", freed)

	return freed, errors.Join(errs...)
}

func migrateAutoVacuum(ctx context.Context, rawDB *sql.DB) error {
	if _, err := rawDB.ExecContext(ctx, "PRAGMA auto_vacuum = INCREMENTAL"); err != nil {
		return fmt.Errorf("set auto_vacuum = INCREMENTAL: %w", err)
	}
	if _, err := rawDB.ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("vacuum database for auto_vacuum migration: %w", err)
	}
	log.Info("retention", "migrated auto_vacuum to INCREMENTAL")
	return nil
}
