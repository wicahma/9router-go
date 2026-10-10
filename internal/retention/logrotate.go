package retention

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"9router/proxy/internal/log"
)

func rotateLogs(ctx context.Context, cfg *Config) error {
	if cfg == nil || cfg.LogMaxBytes <= 0 || len(cfg.LogFiles) == 0 {
		return nil
	}

	var errs []error
	for _, path := range cfg.LogFiles {
		if err := rotateLogFile(path, cfg.LogMaxBytes); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func rotateLogFile(path string, maxBytes int64) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat log file %s: %w", path, err)
	}

	size := info.Size()
	if size <= maxBytes {
		return nil
	}

	keepBytes := maxBytes / 2
	if keepBytes > size {
		keepBytes = size
	}

	buf, err := readTail(path, size, keepBytes)
	if err != nil {
		return err
	}

	if err := writeTruncate(path, buf); err != nil {
		return err
	}

	log.Info("retention", "rotated log file", "path", path, "before_bytes", size, "after_bytes", len(buf))
	return nil
}

func readTail(path string, totalSize, keepBytes int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open log file %s for read: %w", path, err)
	}
	defer f.Close()

	if _, err := f.Seek(totalSize-keepBytes, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek log file %s: %w", path, err)
	}

	buf := make([]byte, keepBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read log file %s: %w", path, err)
	}
	return buf[:n], nil
}

func writeTruncate(path string, data []byte) error {
	wf, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open log file %s for truncate: %w", path, err)
	}
	defer wf.Close()

	if _, err := wf.Write(data); err != nil {
		return fmt.Errorf("write log file %s: %w", path, err)
	}
	if err := wf.Sync(); err != nil {
		return fmt.Errorf("sync log file %s: %w", path, err)
	}
	return nil
}
