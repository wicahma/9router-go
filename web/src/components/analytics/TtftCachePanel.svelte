<script lang="ts">
  import { fmt, fmtMs, type TtftStatsItem, type CacheStatsItem } from './types'

  let { ttft, cache }: { ttft?: TtftStatsItem; cache?: CacheStatsItem } = $props()

  let ttftSamples = $derived(ttft?.samples || 0)
  let firstSample = $derived(ttft?.firstSample || '')
  let cacheRatio = $derived(cache?.hitRatio || 0)
  let topCache = $derived((cache?.byModel || []).slice(0, 6))
  let maxCacheRatio = $derived(Math.max(0.01, ...topCache.map((row) => row.hitRatio || 0)))
  let hasData = $derived(ttftSamples > 0 || (cache?.promptTokens || 0) > 0)

  function fmtDate(iso: string): string {
    if (!iso) return ''
    const d = new Date(iso)
    return d.toLocaleDateString([], { month: 'short', day: 'numeric' })
  }
</script>

<div class="rounded-[14px] border border-border-subtle bg-surface p-4 shadow-[var(--shadow-soft)] sm:p-5">
  <div class="mb-4 flex flex-col gap-1 sm:flex-row sm:items-baseline sm:justify-between">
    <div>
      <h2 class="text-sm font-bold text-text-main">Time to first token &amp; prompt cache</h2>
      <p class="text-xs text-text-muted">
        {#if !hasData}
          No TTFT or cache data in this period.
        {:else if ttftSamples > 0}
          {fmt(ttftSamples)} streaming samples{firstSample ? ` · since ${fmtDate(firstSample)}` : ''} · cache hit
          {(cacheRatio * 100).toFixed(1)}% of {fmt(cache?.promptTokens)} input tokens
        {:else}
          No streaming samples recorded yet · cache hit {(cacheRatio * 100).toFixed(1)}% of {fmt(cache?.promptTokens)} input
          tokens
        {/if}
      </p>
    </div>
  </div>

  {#if hasData}
    <div class="grid min-w-0 grid-cols-1 gap-5 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
      <div class="min-w-0">
        <h3 class="mb-2 text-[11px] font-semibold uppercase tracking-wide text-text-muted">TTFT percentiles</h3>
        {#if ttftSamples === 0}
          <p class="text-xs text-text-muted">
            No streaming samples in this window. TTFT has been recorded since Sep 27 — wider periods only cover the days
            after that.
          </p>
        {:else}
          <div class="grid grid-cols-3 gap-3">
            <div class="rounded-[10px] border border-border-subtle bg-bg px-3 py-2">
              <span class="block text-[10px] font-semibold uppercase tracking-wide text-text-muted">p5</span>
              <span class="block text-lg font-bold text-success">{fmtMs(ttft?.p5Ms)}</span>
            </div>
            <div class="rounded-[10px] border border-border-subtle bg-bg px-3 py-2">
              <span class="block text-[10px] font-semibold uppercase tracking-wide text-text-muted">p50</span>
              <span class="block text-lg font-bold text-text-main">{fmtMs(ttft?.p50Ms)}</span>
            </div>
            <div class="rounded-[10px] border border-border-subtle bg-bg px-3 py-2">
              <span class="block text-[10px] font-semibold uppercase tracking-wide text-text-muted">p95</span>
              <span class="block text-lg font-bold text-warning">{fmtMs(ttft?.p95Ms)}</span>
            </div>
          </div>
          <h3 class="mb-2 mt-4 text-[11px] font-semibold uppercase tracking-wide text-text-muted">
            By model (streaming only)
          </h3>
          <div class="overflow-x-auto">
            <table class="w-full border-collapse text-xs">
              <thead>
                <tr class="border-b border-border text-text-muted">
                  <th class="py-1 text-left font-semibold">Model</th>
                  <th class="py-1 text-right font-semibold">n</th>
                  <th class="py-1 text-right font-semibold">p5</th>
                  <th class="py-1 text-right font-semibold">p50</th>
                  <th class="py-1 text-right font-semibold">p95</th>
                </tr>
              </thead>
              <tbody class="font-mono">
                {#each (ttft?.byModel || []).slice(0, 6) as row}
                  <tr class="border-b border-border/50">
                    <td class="max-w-0 truncate py-1 pr-2 text-left text-text-main" title="{row.model} · {row.provider}"
                      >{row.model}</td
                    >
                    <td class="py-1 text-right text-text-muted">{fmt(row.samples)}</td>
                    <td class="py-1 text-right text-success">{fmtMs(row.p5Ms)}</td>
                    <td class="py-1 text-right text-text-main">{fmtMs(row.p50Ms)}</td>
                    <td class="py-1 text-right text-warning">{fmtMs(row.p95Ms)}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </div>

      <div class="min-w-0">
        <h3 class="mb-2 text-[11px] font-semibold uppercase tracking-wide text-text-muted">Cache hit ratio by model</h3>
        {#if topCache.length === 0}
          <p class="text-xs text-text-muted">No prompt token data recorded.</p>
        {:else}
          <div class="flex flex-col gap-1.5">
            {#each topCache as row}
              <div class="flex items-center gap-2 text-xs">
                <span class="min-w-0 flex-1 truncate font-mono text-text-main" title="{row.model} · {row.provider}"
                  >{row.model}</span
                >
                <div class="h-2 w-24 shrink-0 overflow-hidden rounded-full bg-bg">
                  <div
                    class="h-full rounded-full bg-info/70"
                    style="width: {Math.max(2, ((row.hitRatio || 0) / maxCacheRatio) * 100)}%"
                  ></div>
                </div>
                <span class="w-14 shrink-0 text-right font-mono text-text-main"
                  >{((row.hitRatio || 0) * 100).toFixed(1)}%</span
                >
                <span class="hidden shrink-0 font-mono text-[10px] text-text-muted sm:block"
                  >{fmt(row.cachedTokens)}/{fmt(row.promptTokens)}</span
                >
              </div>
            {/each}
          </div>
          <p class="mt-2 text-[11px] text-text-muted">
            Hit ratio = cached input tokens / input tokens. Reads only — cache writes are not recorded in usage data.
          </p>
        {/if}
      </div>
    </div>
  {/if}
</div>
