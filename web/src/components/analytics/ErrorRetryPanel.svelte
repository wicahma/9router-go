<script lang="ts">
  import { fmt, type ErrorStatsItem } from './types'

  let { errors }: { errors?: ErrorStatsItem } = $props()

  let total = $derived(errors?.total || 0)
  let maxStatus = $derived(Math.max(1, ...(errors?.byStatus || []).map((row) => row.count || 0)))
  let maxAttempts = $derived(Math.max(1, ...(errors?.attempts || []).map((row) => row.requests || 0)))
  let retried = $derived((errors?.attempts || []).filter((row) => (row.attempts || 1) > 1).reduce((acc, row) => acc + (row.requests || 0), 0))
  let burned = $derived(
    (errors?.attempts || []).reduce((acc, row) => acc + (row.requests || 0) * Math.max(0, (row.attempts || 1) - 1), 0),
  )
  let maxTrend = $derived(Math.max(1, ...(errors?.trend || []).map((point) => point.errors || 0)))
</script>

<div class="rounded-[14px] border border-border-subtle bg-surface p-4 shadow-[var(--shadow-soft)] sm:p-5">
  <div class="mb-4 flex flex-col gap-1 sm:flex-row sm:items-baseline sm:justify-between">
    <div>
      <h2 class="text-sm font-bold text-text-main">Errors & retries</h2>
      <p class="text-xs text-text-muted">
        {#if total === 0}
          No failed requests in this period.
        {:else}
          {fmt(total)} failed requests · {fmt(retried)} succeeded after a retry · {fmt(burned)} extra upstream forwards burned
        {/if}
      </p>
    </div>
  </div>

  {#if total > 0}
    <div class="grid min-w-0 grid-cols-1 gap-5 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)]">
      <div class="min-w-0">
        <h3 class="mb-2 text-[11px] font-semibold uppercase tracking-wide text-text-muted">By upstream status</h3>
        <div class="flex flex-col gap-1.5">
          {#each errors?.byStatus || [] as row}
            <div class="flex items-center gap-2 text-xs">
              <span class="w-10 shrink-0 font-mono font-bold text-error">{row.status}</span>
              <div class="h-2 min-w-0 flex-1 overflow-hidden rounded-full bg-bg">
                <div class="h-full rounded-full bg-error/70" style="width: {Math.max(2, ((row.count || 0) / maxStatus) * 100)}%"></div>
              </div>
              <span class="w-12 shrink-0 text-right font-mono text-text-main">{fmt(row.count)}</span>
            </div>
          {/each}
        </div>
        <h3 class="mb-2 mt-4 text-[11px] font-semibold uppercase tracking-wide text-text-muted">Top failing models</h3>
        <ul class="flex flex-col gap-1 text-xs">
          {#each (errors?.byModel || []).slice(0, 5) as row}
            <li class="flex items-baseline justify-between gap-2">
              <span class="min-w-0 truncate font-mono text-text-main" title={row.key}>{row.key}</span>
              <span class="shrink-0 font-mono text-text-muted">{fmt(row.count)}</span>
            </li>
          {/each}
        </ul>
      </div>

      <div class="min-w-0">
        <h3 class="mb-2 text-[11px] font-semibold uppercase tracking-wide text-text-muted">Upstream attempts per request</h3>
        {#if (errors?.attempts || []).length === 0}
          <p class="text-xs text-text-muted">No attempt data recorded yet.</p>
        {:else}
          <div class="flex h-28 items-end gap-1">
            {#each errors?.attempts || [] as row}
              <div class="flex min-w-0 flex-1 flex-col items-center gap-1" title="{fmt(row.requests)} requests · {row.attempts} attempt{(row.attempts || 1) > 1 ? 's' : ''}">
                <div
                  class="w-full max-w-8 rounded-t {(row.attempts || 1) > 1 ? 'bg-warning/80' : 'bg-success/60'}"
                  style="height: {Math.max(3, ((row.requests || 0) / maxAttempts) * 100)}px"
                ></div>
                <span class="font-mono text-[10px] text-text-muted">{row.attempts}</span>
              </div>
            {/each}
          </div>
          <p class="mt-1 text-[11px] text-text-muted">Green = first try worked · amber = retries were needed</p>
          {#if (errors?.byProvider || []).length > 0}
            <h3 class="mb-2 mt-4 text-[11px] font-semibold uppercase tracking-wide text-text-muted">Top failing providers</h3>
            <ul class="flex flex-col gap-1 text-xs">
              {#each (errors?.byProvider || []).slice(0, 5) as row}
                <li class="flex items-baseline justify-between gap-2">
                  <span class="min-w-0 truncate text-text-main" title={row.key}>{row.key}</span>
                  <span class="shrink-0 font-mono text-text-muted">{fmt(row.count)}</span>
                </li>
              {/each}
            </ul>
          {/if}
        {/if}
      </div>

      <div class="min-w-0">
        <h3 class="mb-2 text-[11px] font-semibold uppercase tracking-wide text-text-muted">Failures over time</h3>
        {#if (errors?.trend || []).length === 0}
          <p class="text-xs text-text-muted">No trend data.</p>
        {:else}
          <div class="overflow-x-auto">
            <svg viewBox="0 0 300 110" class="h-28 min-w-[220px] w-full" role="img" aria-label="Error trend">
              <line x1="4" y1="92" x2="296" y2="92" class="stroke-border" />
              {#each (errors?.trend || []) as point, i}
                {@const h = point.errors > 0 ? Math.max(2, ((point.errors || 0) / maxTrend) * 80) : 0}
                {@const w = 292 / Math.max((errors?.trend || []).length, 1)}
                <rect x={4 + i * w} y={92 - h} width={Math.max(1, w - 2)} height={h} rx="1" class="fill-error/70">
                  <title>{new Date(point.timestamp).toLocaleString([], { month: 'short', day: 'numeric', hour: 'numeric' })} · {fmt(point.errors)} errors</title>
                </rect>
              {/each}
            </svg>
          </div>
        {/if}
      </div>
    </div>
  {/if}
</div>
