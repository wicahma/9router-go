<script lang="ts">
  import Button from '../../lib/ui/Button.svelte'

  interface Props {
    onCreateClick: () => void
  }

  let { onCreateClick }: Props = $props()
</script>

<div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
  <div class="min-w-0">
    <p class="text-sm text-text-muted mt-1">
      Group models under one name, then pick a strategy per combo:
    </p>
    <ul class="text-sm text-text-muted mt-2 flex flex-col gap-1">
      <li>
        <span class="font-medium text-text-main">Fallback</span> — tries models in order (next on failure)
      </li>
      <li>
        <span class="font-medium text-text-main">Round Robin</span> — rotates models across requests to spread load
      </li>
      <li>
        <span class="font-medium text-text-main">Fusion</span> — queries all models in parallel, then a judge
        synthesizes one answer. Best quality, but costs the most: every request bills all panel models + the judge
        (N+1 calls)
      </li>
      <li>
        <span class="font-medium text-text-main">Sticky</span> — pins a model for N consecutive requests, then
        rotates. Same-model follow-ups keep cache warm; lower latency than round robin
      </li>
      <li>
        <span class="font-medium text-text-main">Capacity</span> — prefers models with free-tier headroom, falling
        back in order when quota is exhausted. Disabled models are skipped in every strategy
      </li>
    </ul>
  </div>
  <div class="flex w-full flex-col gap-2 sm:w-auto sm:items-stretch">
    <Button icon="add" onclick={onCreateClick} class="w-full sm:w-auto whitespace-nowrap">
      Create Combo
    </Button>
  </div>
</div>
