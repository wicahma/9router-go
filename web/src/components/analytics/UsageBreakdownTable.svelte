<script lang="ts">
  import Badge from '../../lib/ui/Badge.svelte'
  import Card from '../../lib/ui/Card.svelte'
  import { getIconPath } from '../connections/types'
  import {
    fmt,
    fmtCost,
    fmtMs,
    timeAgo,
    TABLE_OPTIONS,
    type StatsData,
    type TableView,
    type ViewMode,
    type UsageItem
  } from './types'

  let { stats = {} }: { stats?: StatsData } = $props()

  let tableView = $state<TableView>('model')
  let viewMode = $state<ViewMode>('costs')

  // Latency has no per-account/key/endpoint percentiles, so that mode always
  // reads the model table — labels must follow the data, not the dropdown.
  let view = $derived(viewMode === 'latency' ? 'model' : tableView)

  function label(row: ProcessedUsageRow): string {
    if (view === 'account') return row.accountName || row.key
    if (view === 'apiKey') return row.keyName || row.key
    if (view === 'endpoint') return row.endpoint || row.key
    return row.rawModel || row.key
  }

  interface ProcessedUsageRow extends UsageItem {
    key: string
    totalTokens: number
  }

  let tableData = $derived((): ProcessedUsageRow[] => {
    if (!stats) return []
    // Percentiles are only computed per model+provider, so the latency view is
    // always the model table regardless of the dropdown.
    let sourceMap: Record<string, UsageItem> = {}
    if (view === 'model') sourceMap = stats.byModel || {}
    else if (view === 'account') sourceMap = stats.byAccount || {}
    else if (view === 'apiKey') sourceMap = stats.byApiKey || {}
    else if (view === 'endpoint') sourceMap = stats.byEndpoint || {}

    const rows = Object.entries(sourceMap).map(([key, item]) => {
      const totalTokens = (item.promptTokens || 0) + (item.completionTokens || 0)
      return {
        key,
        ...item,
        totalTokens,
      }
    })

    if (viewMode === 'latency') {
      // Models with no recorded durations sort last instead of topping the list
      // with zeros.
      return rows.sort((a, b) => (b.p95Ms || 0) - (a.p95Ms || 0))
    }
    return rows.sort((a, b) => (b.requests || 0) - (a.requests || 0))
  })
</script>

<div class="flex flex-col gap-3 pt-2">
  <div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
    <!-- View selector dropdown -->
    <select
      bind:value={tableView}
      disabled={viewMode === 'latency'}
      class="w-full sm:w-auto rounded-lg border border-border bg-surface px-3 py-1.5 text-sm font-semibold text-text-main focus:outline-none focus:ring-2 focus:ring-brand-500/50 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
    >
      {#each TABLE_OPTIONS as opt}
        <option value={opt.value}>{opt.label}</option>
      {/each}
    </select>

    <!-- Toggle: Costs | Tokens | Latency -->
    <div class="inline-flex rounded-xl bg-surface border border-border p-1 shadow-sm self-start sm:self-auto">
      {#each [{ id: 'costs', label: 'Costs' }, { id: 'tokens', label: 'Tokens' }, { id: 'latency', label: 'Latency' }] as m}
        <button
          type="button"
          onclick={() => (viewMode = m.id as ViewMode)}
          class="rounded-lg px-3 py-1 text-xs font-semibold transition-colors cursor-pointer {viewMode === m.id
            ? 'bg-brand-500 text-white shadow-sm'
            : 'text-text-muted hover:text-text-main'}"
        >
          {m.label}
        </button>
      {/each}
    </div>
  </div>

  <!-- Breakdown Table Card -->
  <Card padding="none" class="overflow-hidden border border-border">
    {#if tableData().length === 0}
      <div class="p-8 text-center text-text-muted text-sm font-body">
        No usage recorded for this period.
      </div>
    {:else if viewMode === 'latency' && !tableData().some((r) => (r.latencySamples || 0) > 0)}
      <div class="p-8 text-center text-text-muted text-sm font-body">
        No request timings recorded yet.
      </div>
    {:else}
      <div class="overflow-x-auto">
        <table class="w-full text-left border-collapse text-xs font-body">
          <thead class="bg-surface-2 border-b border-border text-text-muted uppercase text-[10px] font-semibold tracking-wider">
            <tr>
              <th class="py-3 px-4">
                {view === 'model' ? 'Model' : view === 'account' ? 'Account' : view === 'apiKey' ? 'Key Name' : 'Endpoint'}
              </th>
              <th class="py-3 px-4">Provider</th>
              <th class="py-3 px-4 text-right">Requests</th>
              {#if viewMode === 'costs'}
                <th class="py-3 px-4 text-right">Total Cost</th>
              {:else if viewMode === 'latency'}
                <th class="py-3 px-4 text-right">p50</th>
                <th class="py-3 px-4 text-right">p95</th>
                <th class="py-3 px-4 text-right">p99</th>
                <th class="py-3 px-4 text-right">Samples</th>
              {:else}
                <th class="py-3 px-4 text-right">In Tokens</th>
                <th class="py-3 px-4 text-right">Out Tokens</th>
                <th class="py-3 px-4 text-right">Cached Tokens</th>
              {/if}
              <th class="py-3 px-4 text-right">Last Used</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border/60">
            {#each tableData() as row}
              <tr class="hover:bg-surface-2/60 transition-colors">
                <td class="py-3 px-4 font-mono font-medium text-text-main text-xs">
                  <div class="flex items-center gap-2">
                    {#if row.provider}
                      <img
                        src={getIconPath(row.provider)}
                        alt={row.provider}
                        class="w-4 h-4 object-contain rounded shrink-0 bg-surface-2 p-0.5 border border-border/40"
                        onerror={(e) => {
                          (e.currentTarget as HTMLElement).style.display = 'none'
                        }}
                        loading="lazy"
                      />
                    {/if}
                    <span class="truncate">{label(row)}</span>
                  </div>
                </td>
                <td class="py-3 px-4">
                  <div class="flex items-center gap-1.5">
                    {#if row.provider}
                      <img
                        src={getIconPath(row.provider)}
                        alt={row.provider}
                        class="w-3.5 h-3.5 object-contain rounded shrink-0"
                        onerror={(e) => {
                          (e.currentTarget as HTMLElement).style.display = 'none'
                        }}
                        loading="lazy"
                      />
                    {/if}
                    <Badge variant="neutral" size="sm">{row.provider || 'unknown'}</Badge>
                  </div>
                </td>
                <td class="py-3 px-4 text-right font-mono font-semibold text-text-main">
                  {fmt(row.requests)}
                </td>
                {#if viewMode === 'costs'}
                  <td class="py-3 px-4 text-right font-mono font-bold text-warning">
                    {fmtCost(row.cost)}
                  </td>
                {:else if viewMode === 'latency'}
                  <td class="py-3 px-4 text-right font-mono text-text-main">{fmtMs(row.p50Ms)}</td>
                  <td class="py-3 px-4 text-right font-mono font-semibold text-warning">{fmtMs(row.p95Ms)}</td>
                  <td class="py-3 px-4 text-right font-mono text-danger">{fmtMs(row.p99Ms)}</td>
                  <td class="py-3 px-4 text-right font-mono text-text-muted">{fmt(row.latencySamples)}</td>
                {:else}
                  <td class="py-3 px-4 text-right font-mono text-brand-500">
                    {fmt(row.promptTokens)}
                  </td>
                  <td class="py-3 px-4 text-right font-mono text-success">
                    {fmt(row.completionTokens)}
                  </td>
                  <td class="py-3 px-4 text-right font-mono text-info">
                    {fmt(row.cachedTokens)}
                  </td>
                {/if}
                <td class="py-3 px-4 text-right text-text-muted whitespace-nowrap text-[11px]">
                  {timeAgo(row.lastUsed)}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </Card>
</div>
