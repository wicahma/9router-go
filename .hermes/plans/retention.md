# 9router-go — Log & Data Retention

## Masalah

Disk `/home` **90%** (7.8 GB sisa). Penyumbang dari 9router:

| Item | Ukuran |
|---|---|
| `requestDetails` (70k rows, avg 12.8 KB `data`) | **897 MB** |
| `usageHistory` (100k rows) | 53 MB |
| `kv` | 17 MB |
| `db/backups/data.sqlite.pre-go-share-*` (stale 2026-09-25) | 847 MB |
| 30 stale `9router-go.bak-*` binary | 851 MB |
| `/var/log/9router-go-error.log` | 6.4 MB |

Belum ada retention apa pun. `auto_vacuum = 0` → hapus row **tidak** mengecilkan file.
Log dipegang systemd `StandardOutput=append:` → systemd tidak pernah merotasi.

## Desain

### Env (semua opsional, ada default)

```
RETENTION_ENABLED=true
RETENTION_INTERVAL=15m
RETENTION_DB_MAX_MB=256
RETENTION_USAGE_HISTORY_MAX_AGE_HOURS=720
RETENTION_REQUEST_DETAILS_MAX_AGE_HOURS=72
RETENTION_REQUEST_DETAILS_MAX_ROWS=20000
RETENTION_LOG_MAX_MB=16
RETENTION_LOG_FILES=/var/log/9router-go.log,/var/log/9router-go-error.log
```

### Paket baru `internal/retention/`

- `Config` — hasil parse env, diisi `config.Config.Retention`
- `Run(ctx, repo, cfg) error` — satu pass:
  1. `DELETE FROM requestDetails WHERE timestamp < cutoff` (age)
  2. `DELETE FROM requestDetails` sisakan N terbaru (row cap, `id NOT IN (SELECT id ... ORDER BY timestamp DESC LIMIT N)`)
  3. `DELETE FROM usageHistory WHERE timestamp < cutoff`
  4. `incremental_vacuum` sampai `page_count*page_size <= DB_MAX`
  5. rotasi tiap file di `RETENTION_LOG_FILES` yang > `LOG_MAX_MB`
- `StartBackground(ctx, repo, cfg)` — ticker, pola sama `StartBackgroundCatalogSync`

### Migrasi auto_vacuum (wajib, sekali)

`auto_vacuum=0` → `DELETE` tidak melepas halaman. Deteksi `PRAGMA auto_vacuum != 2`,
lalu `PRAGMA auto_vacuum=INCREMENTAL` + `VACUUM` penuh sekali. Setelah itu
`PRAGMA incremental_vacuum(N)` melepas halaman bertahap tanpa rewrite penuh.

Butuh ruang disk ~seukuran DB saat VACUUM. 7.8 GB free, DB 1 GB — aman.

### Rotasi log

`StandardOutput=append:` → fd dipegang systemd. **Rename tidak berfungsi**
(systemd lanjut nulis ke inode lama). Jadi: baca tail N byte → `os.Truncate(0)` →
tulis balik tail. Race window kecil, bisa hilang beberapa baris — dapat diterima
untuk log.

`/var/log/9router-go-error.log` owner `root:root 0644` → proses user `9router`
**tidak bisa** menulisnya. Perlu perbaikan sekali di luar app (chown 9router).

## Verifikasi

- `go build ./...` ok
- `go test ./internal/retention/... ./internal/config/...` PASS
- test table-driven: parse env, cap baris, cutoff usia, no-op saat disabled
- live: panggil `Run` sekali → ukuran DB turun, `PRAGMA page_count` turun
- live: 10x chat masih 200 setelah retention jalan

## Di luar scope (ops, minta izin dulu)

- Hapus 847 MB `db/backups/data.sqlite.pre-go-share-*` (stale sejak 2026-09-25)
- Hapus 30 stale `9router-go.bak-*` (851 MB)
- chown `9router` pada `/var/log/9router-go-error.log`

## Status

- [ ] Plan ditulis
- [ ] Pi brief disubmit
- [ ] Verifikasi mandiri
- [ ] Commit + push
