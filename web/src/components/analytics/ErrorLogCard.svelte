<script lang="ts">
  import { onMount } from 'svelte'
  import { api } from '../../api/client'
  import { loadProviderNames, resolveProviderName } from '../../lib/providerNames'
  import { fmt, timeAgo } from './types'

  interface ErrorRow {
    timestamp?: string
    provider?: string
    model?: string
    status?: string
    attempts?: number
    response?: { status?: number; error?: string }
    tokens?: { prompt_tokens?: number; completion_tokens?: number }
  }

  let rows = $state<ErrorRow[]>([])
  let total = $state(0)
  let loading = $state(true)
  let failed = $state(false)
  let busy = $state(false)
  let nodeNames = $state<Record<string, string>>({})

  $effect(() => {
    loadProviderNames().then((n) => (nodeNames = n))
  })

  async function load() {
    if (busy) return
    busy = true
    loading = true
    failed = false
    try {
      const res = await api.getRequestDetails(15, 0, 'error')
      rows = (res?.details || []) as ErrorRow[]
      total = res?.total || 0
    } catch {
      // Keep the last good rows so a transient failure doesn't blank the log;
      // surface it honestly rather than pretending there are no errors.
      failed = true
    } finally {
      busy = false
      loading = false
    }
  }

  onMount(() => {
    load()
    const timer = setInterval(load, 20000)
    return () => clearInterval(timer)
  })

  function providerLabel(p?: string): string {
    return resolveProviderName(p, nodeNames)
  }
</script>

<div class="bg-surface border border-border-subtle rounded-[14px] shadow-[var(--shadow-soft)] p-4 flex min-w-0 flex-col gap-2.5">
  <div class="flex items-center justify-between px-1">
    <span class="text-xs font-semibold text-text-muted uppercase tracking-wide">Error Log</span>
    <span class="text-[11px] font-mono text-error">{fmt(total)} total</span>
  </div>

  {#if loading}
    <div class="py-6 text-center text-xs text-text-muted">Loading…</div>
  {:else if failed && rows.length === 0}
    <div class="py-6 text-center text-xs text-text-muted">Error log unavailable — retrying.</div>
  {:else if rows.length === 0}
    <div class="py-6 text-center text-xs text-text-muted">No errors recorded.</div>
  {:else}
    <div class="flex flex-col gap-2 max-h-[320px] overflow-y-auto pr-1">
      {#each rows as r, i (r.timestamp || i)}
        <div class="rounded-lg border border-error/20 bg-error/5 px-3 py-2 flex flex-col gap-1 min-w-0">
          <div class="flex items-center gap-2 min-w-0">
            <span class="px-1.5 py-0.5 text-[10px] font-mono font-semibold rounded bg-error/15 text-error border border-error/30 shrink-0">
              {r.response?.status || r.status || 'ERR'}
            </span>
            <span class="text-[11px] font-mono text-text-main truncate" title={r.model}>{r.model || '—'}</span>
            <span class="text-[10px] font-mono text-text-muted truncate" title={r.provider}>{providerLabel(r.provider)}</span>
            <span class="ml-auto text-[10px] text-text-muted whitespace-nowrap shrink-0">{timeAgo(r.timestamp)}</span>
          </div>
          {#if r.response?.error}
            <p class="text-[11px] text-text-muted break-words line-clamp-2">{r.response.error}</p>
          {/if}
          <div class="flex items-center gap-3 text-[10px] font-mono text-text-muted">
            <span>in <span class="text-text-main">{fmt(r.tokens?.prompt_tokens)}</span></span>
            <span>out <span class="text-text-main">{fmt(r.tokens?.completion_tokens)}</span></span>
            <span>attempts <span class="text-text-main">{r.attempts || 0}</span></span>
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>
