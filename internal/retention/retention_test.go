package retention

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"9router/proxy/internal/db"
)

func setupTestRepo(t *testing.T) *db.Repo {
	t.Helper()
	conn, err := db.OpenDatabase(":memory:")
	if err != nil {
		t.Fatalf("OpenDatabase failed: %v", err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { conn.Close() })

	if err := db.EnsureCoreSchema(conn); err != nil {
		t.Fatalf("EnsureCoreSchema failed: %v", err)
	}

	return db.NewRepo(conn)
}

func TestPruneRequestDetailsAgeCutoff(t *testing.T) {
	repo := setupTestRepo(t)
	ctx := context.Background()

	oldTS := time.Now().UTC().Add(-100 * time.Hour).Format("2006-01-02T15:04:05.000Z")
	midTS := time.Now().UTC().Add(-10 * time.Hour).Format("2006-01-02T15:04:05.000Z")
	newTS := time.Now().UTC().Add(-1 * time.Hour).Format("2006-01-02T15:04:05.000Z")

	rows := []struct {
		id string
		ts string
	}{
		{"row-old", oldTS},
		{"row-mid", midTS},
		{"row-new", newTS},
	}
	for _, r := range rows {
		if _, err := repo.RawDB().Exec("INSERT INTO requestDetails (id, timestamp, data) VALUES (?, ?, ?)", r.id, r.ts, "{}"); err != nil {
			t.Fatalf("failed inserting test row: %v", err)
		}
	}

	cfg := &Config{
		RequestDetailsMaxAge:  72 * time.Hour,
		RequestDetailsMaxRows: 0,
	}

	n, err := pruneRequestDetails(ctx, repo, cfg)
	if err != nil {
		t.Fatalf("pruneRequestDetails failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 pruned row, got %d", n)
	}

	var count int
	if err := repo.RawDB().QueryRow("SELECT COUNT(*) FROM requestDetails").Scan(&count); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 remaining rows, got %d", count)
	}

	var remainingID string
	err = repo.RawDB().QueryRow("SELECT id FROM requestDetails WHERE id = 'row-old'").Scan(&remainingID)
	if err == nil {
		t.Errorf("expected row-old to be deleted, but it was found")
	}
}

func TestPruneRequestDetailsMaxRows(t *testing.T) {
	repo := setupTestRepo(t)
	ctx := context.Background()

	now := time.Now().UTC()
	for i := 1; i <= 5; i++ {
		ts := now.Add(time.Duration(-i) * time.Hour).Format("2006-01-02T15:04:05.000Z")
		id := filepath.Join("id", time.Duration(-i).String())
		if _, err := repo.RawDB().Exec("INSERT INTO requestDetails (id, timestamp, data) VALUES (?, ?, ?)", id, ts, "{}"); err != nil {
			t.Fatalf("failed inserting test row: %v", err)
		}
	}

	cfg := &Config{
		RequestDetailsMaxAge:  0,
		RequestDetailsMaxRows: 3,
	}

	n, err := pruneRequestDetails(ctx, repo, cfg)
	if err != nil {
		t.Fatalf("pruneRequestDetails failed: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 pruned rows, got %d", n)
	}

	var count int
	if err := repo.RawDB().QueryRow("SELECT COUNT(*) FROM requestDetails").Scan(&count); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 remaining rows, got %d", count)
	}
}

func TestPruneDisabledLimits(t *testing.T) {
	repo := setupTestRepo(t)
	ctx := context.Background()

	oldTS := time.Now().UTC().Add(-1000 * time.Hour).Format("2006-01-02T15:04:05.000Z")
	for _, id := range []string{"r1", "r2"} {
		if _, err := repo.RawDB().Exec("INSERT INTO requestDetails (id, timestamp, data) VALUES (?, ?, ?)", id, oldTS, "{}"); err != nil {
			t.Fatalf("failed inserting requestDetails: %v", err)
		}
	}

	oldRFC := time.Now().UTC().Add(-1000 * time.Hour).Format(time.RFC3339)
	for i := 0; i < 2; i++ {
		if _, err := repo.RawDB().Exec("INSERT INTO usageHistory (timestamp) VALUES (?)", oldRFC); err != nil {
			t.Fatalf("failed inserting usageHistory: %v", err)
		}
	}

	cfg := &Config{
		RequestDetailsMaxAge:  0,
		RequestDetailsMaxRows: 0,
		UsageHistoryMaxAge:    0,
	}

	nReq, err := pruneRequestDetails(ctx, repo, cfg)
	if err != nil {
		t.Fatalf("pruneRequestDetails failed: %v", err)
	}
	if nReq != 0 {
		t.Errorf("expected 0 pruned requestDetails, got %d", nReq)
	}

	nUsage, err := pruneUsageHistory(ctx, repo, cfg)
	if err != nil {
		t.Fatalf("pruneUsageHistory failed: %v", err)
	}
	if nUsage != 0 {
		t.Errorf("expected 0 pruned usageHistory, got %d", nUsage)
	}
}

func TestPruneUsageHistoryAgeCutoff(t *testing.T) {
	repo := setupTestRepo(t)
	ctx := context.Background()

	oldTS := time.Now().UTC().Add(-100 * time.Hour).Format(time.RFC3339)
	midTS := time.Now().UTC().Add(-10 * time.Hour).Format(time.RFC3339)
	newTS := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)

	for _, ts := range []string{oldTS, midTS, newTS} {
		if _, err := repo.RawDB().Exec("INSERT INTO usageHistory (timestamp) VALUES (?)", ts); err != nil {
			t.Fatalf("failed inserting usageHistory: %v", err)
		}
	}

	cfg := &Config{
		UsageHistoryMaxAge: 72 * time.Hour,
	}

	n, err := pruneUsageHistory(ctx, repo, cfg)
	if err != nil {
		t.Fatalf("pruneUsageHistory failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 pruned usageHistory row, got %d", n)
	}

	var count int
	if err := repo.RawDB().QueryRow("SELECT COUNT(*) FROM usageHistory").Scan(&count); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 remaining rows, got %d", count)
	}
}

func TestRunDisabled(t *testing.T) {
	repo := setupTestRepo(t)
	ctx := context.Background()

	oldTS := time.Now().UTC().Add(-100 * time.Hour).Format("2006-01-02T15:04:05.000Z")
	if _, err := repo.RawDB().Exec("INSERT INTO requestDetails (id, timestamp, data) VALUES (?, ?, ?)", "rd1", oldTS, "{}"); err != nil {
		t.Fatalf("failed inserting requestDetails: %v", err)
	}

	oldRFC := time.Now().UTC().Add(-100 * time.Hour).Format(time.RFC3339)
	if _, err := repo.RawDB().Exec("INSERT INTO usageHistory (timestamp) VALUES (?)", oldRFC); err != nil {
		t.Fatalf("failed inserting usageHistory: %v", err)
	}

	cfg := &Config{
		Enabled:              false,
		RequestDetailsMaxAge: 1 * time.Hour,
		UsageHistoryMaxAge:   1 * time.Hour,
	}

	err := Run(ctx, repo, cfg)
	if err != nil {
		t.Fatalf("expected nil error on disabled Run, got %v", err)
	}

	var rdCount, uhCount int
	_ = repo.RawDB().QueryRow("SELECT COUNT(*) FROM requestDetails").Scan(&rdCount)
	_ = repo.RawDB().QueryRow("SELECT COUNT(*) FROM usageHistory").Scan(&uhCount)

	if rdCount != 1 || uhCount != 1 {
		t.Errorf("expected row counts to remain 1, got rdCount=%d, uhCount=%d", rdCount, uhCount)
	}
}

func TestRotateLogsShrinksOversizedFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	data := append(bytes.Repeat([]byte("A"), 50), bytes.Repeat([]byte("B"), 50)...)
	if err := os.WriteFile(logPath, data, 0644); err != nil {
		t.Fatalf("failed to write test log file: %v", err)
	}

	cfg := &Config{
		LogMaxBytes: 50,
		LogFiles:    []string{logPath},
	}

	if err := rotateLogs(context.Background(), cfg); err != nil {
		t.Fatalf("rotateLogs failed: %v", err)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read rotated log file: %v", err)
	}

	if len(got) != 25 {
		t.Errorf("expected rotated size 25, got %d", len(got))
	}
	expected := bytes.Repeat([]byte("B"), 25)
	if !bytes.Equal(got, expected) {
		t.Errorf("expected rotated log content to be newest 25 bytes of B, got %s", string(got))
	}
}

func TestRotateLogsUnderLimitUnchanged(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	data := bytes.Repeat([]byte("X"), 40)
	if err := os.WriteFile(logPath, data, 0644); err != nil {
		t.Fatalf("failed to write test log file: %v", err)
	}

	cfg := &Config{
		LogMaxBytes: 50,
		LogFiles:    []string{logPath},
	}

	if err := rotateLogs(context.Background(), cfg); err != nil {
		t.Fatalf("rotateLogs failed: %v", err)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}

	if !bytes.Equal(got, data) {
		t.Errorf("expected log file content to remain byte-identical")
	}
}
