<script lang="ts">
  import type { FlightItem } from './types'
  import { loadProviderNames, resolveProviderName } from '../../lib/providerNames'

  interface Props {
    flights?: FlightItem[]
  }

  let { flights = [] }: Props = $props()

  let providerNames = $state<Record<string, string>>({})

  $effect(() => {
    loadProviderNames().then((n) => (providerNames = n))
  })

  // Phase order drives both the colour ramp and the sort, so a row's position
  // in the list reads as "how far along" without a legend to decode.
  const PHASE_ORDER: Record<string, number> = {
    queue: 0,
    db: 1,
    upstream: 2,
    stream: 3,
  }

  const PHASE_LABEL: Record<string, string> = {
    queue: 'QUEUE',
    db: 'DB',
    upstream: 'UPSTREAM',
    stream: 'STREAM',
  }

  const PHASE_CLASS: Record<string, string> = {
    queue: 'text-warn',
    db: 'text-error',
    upstream: 'text-info',
    stream: 'text-success',
  }

  // A phase that has run far longer than the rest is the whole point of the
  // panel, so surface it rather than requiring the reader to compare numbers.
  const STALL_MS = 15_000

  let now = $state(Date.now())
  // Time of the last server push. phaseMs is only recomputed when a push
  // arrives, so elapsed time since then is added locally to keep the counter
  // moving without asking the server any more often than it already speaks.
  let pushedAt = $state(Date.now())

  $effect(() => {
    const t = setInterval(() => (now = Date.now()), 1000)
    return () => clearInterval(t)
  })

  $effect(() => {
    if (flights.length > 0) pushedAt = Date.now()
  })

  function livePhaseMs(f: FlightItem): number {
    return (f.phaseMs || 0) + Math.max(0, now - pushedAt)
  }

  function secs(ms: number): string {
    if (ms < 1000) return '0s'
    const s = Math.floor(ms / 1000)
    if (s < 60) return `${s}s`
    const m = Math.floor(s / 60)
    return `${m}m${String(s % 60).padStart(2, '0')}s`
  }

  const sorted = $derived(
    [...flights].sort((a, b) => {
      const pa = PHASE_ORDER[a.phase || ''] ?? 9
      const pb = PHASE_ORDER[b.phase || ''] ?? 9
      if (pa !== pb) return pa - pb
      return (b.phaseMs || 0) - (a.phaseMs || 0)
    }),
  )

  const stalled = $derived(sorted.filter((f) => livePhaseMs(f) >= STALL_MS).length)
</script>

<div class="bg-surface border border-border-subtle rounded-[14px] shadow-[var(--shadow-soft)] p-4 flex min-w-0 flex-col overflow-hidden" style="height: 480px">
  <div class="px-1 py-2 border-b border-border shrink-0 flex items-center justify-between">
    <span class="text-xs font-semibold text-text-muted uppercase tracking-wide">In-Flight</span>
    <span class="text-[10px] font-mono {stalled > 0 ? 'text-error' : 'text-text-muted'}">
      {sorted.length} live{stalled > 0 ? ` · ${stalled} stalled` : ''}
    </span>
  </div>

  {#if sorted.length === 0}
    <div class="flex-1 flex items-center justify-center text-text-muted text-xs">
      No requests in flight.
    </div>
  {:else}
    <div class="flex-1 overflow-y-auto">
      {#each sorted as f (f.id)}
        {@const phaseMs = livePhaseMs(f)}
        {@const isStalled = phaseMs >= STALL_MS}
        <div class="px-1 py-2 border-b border-border/40 min-w-0">
          <div class="flex items-center justify-between gap-2 min-w-0">
            <span class="text-[10px] font-mono font-semibold shrink-0 {PHASE_CLASS[f.phase || ''] || 'text-text-muted'}">
              {PHASE_LABEL[f.phase || ''] || (f.phase || '?').toUpperCase()}
            </span>
            <span class="text-[10px] font-mono shrink-0 {isStalled ? 'text-error font-semibold' : 'text-text-muted'}">
              {secs(phaseMs)}
            </span>
          </div>
          <div class="mt-0.5 truncate text-[11px] font-mono" title="{f.model || ''} · {resolveProviderName(f.provider, providerNames)}">
            <span>{f.model || '—'}</span>
            <span class="text-text-muted"> · </span>
            <span class="text-text-muted">{resolveProviderName(f.provider, providerNames)}</span>
          </div>
          <div class="mt-0.5 flex items-center justify-between gap-2 text-[10px] font-mono text-text-muted min-w-0">
            <span class="truncate" title={f.detail || f.account || ''}>{f.detail || f.account || f.id || ''}</span>
            <span class="shrink-0">age {secs(f.ageMs || 0)}{f.attempt && f.attempt > 1 ? ` · try ${f.attempt}` : ''}</span>
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>
