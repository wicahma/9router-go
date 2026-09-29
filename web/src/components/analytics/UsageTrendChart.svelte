<script lang="ts">
  import { fmt, type UsageTrendItem } from './types'

  let { trend = [], period = 'today' }: { trend?: UsageTrendItem[]; period?: string } = $props()
  let metric = $state<'requests' | 'tokens' | 'cost' | 'latency'>('requests')
  const options = [
    { value: 'requests', label: 'Requests' },
    { value: 'tokens', label: 'Tokens' },
    { value: 'cost', label: 'Cost' },
    { value: 'latency', label: 'Avg latency' },
  ] as const

  let bars = $derived.by(() => {
    const values = trend.map((item) => {
      const value = metric === 'requests' ? item.requests || 0
        : metric === 'tokens' ? (item.promptTokens || 0) + (item.completionTokens || 0)
        : metric === 'cost' ? item.cost || 0
        : item.avgLatencyMs || 0
      return { item, value }
    })
    const max = Math.max(1, ...values.map((entry) => entry.value))
    return values.map(({ item, value }, index) => ({
      item,
      value,
      x: 36 + index * (700 / Math.max(values.length, 1)),
      width: Math.max(2, 700 / Math.max(values.length, 1) - 4),
      height: value > 0 ? Math.max(2, (value / max) * 150) : 0,
    }))
  })

  function title(timestamp: string): string {
    const date = new Date(timestamp)
    return period === 'today' || period === '24h'
      ? date.toLocaleString([], { month: 'short', day: 'numeric', hour: 'numeric' })
      : date.toLocaleDateString([], { month: 'short', day: 'numeric' })
  }

  function display(value: number): string {
    if (metric === 'cost') return '$' + value.toFixed(2)
    if (metric === 'latency') return Math.round(value) + ' ms'
    return fmt(value)
  }
</script>

<div class="rounded-[14px] border border-border-subtle bg-surface p-4 shadow-[var(--shadow-soft)] sm:p-5">
  <div class="mb-4 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
    <div>
      <h2 class="text-sm font-bold text-text-main">Usage over time</h2>
      <p class="text-xs text-text-muted">{period === 'today' || period === '24h' ? 'Hourly' : 'Daily'} totals · UTC</p>
    </div>
    <div class="flex flex-wrap gap-1 rounded-lg border border-border bg-bg p-1" role="group" aria-label="Trend metric">
      {#each options as option}
        <button
          type="button"
          aria-pressed={metric === option.value}
          onclick={() => (metric = option.value)}
          class="rounded-md px-2.5 py-1.5 text-xs font-medium transition-colors {metric === option.value ? 'bg-brand-500 text-white' : 'text-text-muted hover:text-text-main'}"
        >
          {option.label}
        </button>
      {/each}
    </div>
  </div>
  {#if bars.length === 0}
    <div class="flex h-44 items-center justify-center text-xs text-text-muted">No trend data in this period.</div>
  {:else}
    <div class="overflow-x-auto">
      <svg viewBox="0 0 760 210" class="h-44 min-w-[520px] w-full" role="img" aria-label="Usage trend bar chart">
        <line x1="36" y1="180" x2="736" y2="180" class="stroke-border" />
        {#each bars as bar (bar.item.timestamp)}
          <rect
            x={bar.x}
            y={180 - bar.height}
            width={bar.width}
            height={bar.height}
            rx="2"
            class="fill-brand-500/80 hover:fill-brand-500"
          >
            <title>{title(bar.item.timestamp)} · {display(bar.value)}</title>
          </rect>
        {/each}
        {#each bars.filter((_, i) => i === 0 || i === bars.length - 1 || i === Math.floor(bars.length / 2)) as bar (bar.item.timestamp)}
          <text x={bar.x} y="202" class="fill-text-muted" font-size="10">{title(bar.item.timestamp)}</text>
        {/each}
      </svg>
    </div>
    <div class="mt-1 text-right text-xs font-semibold tabular-nums text-text-main">
      Peak: {display(Math.max(0, ...bars.map((bar) => bar.value)))}
    </div>
  {/if}
</div>
