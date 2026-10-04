<script lang="ts">
  import {
    ArrowDown,
    ArrowUp,
    Brain,
    Eye,
    GripVertical,
    Layers,
    Plus,
    X
  } from 'lucide-svelte'
  import type { Combo } from '../../api/client'
  import { getModelCaps } from '../../lib/models'
  import Button from '../../lib/ui/Button.svelte'
  import Input from '../../lib/ui/Input.svelte'
  import Modal from '../../lib/ui/Modal.svelte'
  import { getModelLimit, parseLimitInput } from './types'

  interface Props {
    isOpen: boolean
    editingCombo: Combo | null
    models: string[]
    modelRps?: Record<string, number>
    modelContextLimit?: Record<string, number>
    isSaving?: boolean
    onClose: () => void
    onSave: (name: string, models: string[]) => Promise<void> | void
    onOpenModelPicker: () => void
    onUpdateModels: (models: string[]) => void
    onSetModelRps: (model: string, rps: number) => void
    onSetModelContextLimit: (model: string, tokens: number) => void
  }

  let {
    isOpen,
    editingCombo,
    models,
    modelRps = {},
    modelContextLimit = {},
    isSaving = false,
    onClose,
    onSave,
    onOpenModelPicker,
    onUpdateModels,
    onSetModelRps,
    onSetModelContextLimit,
  }: Props = $props()

  let modalName = $state(editingCombo?.name || '')
  let modalNameError = $state('')
  let editingIdx = $state<number | null>(null)
  let editDraft = $state('')
  let dragIdx = $state<number | null>(null)
  let overIdx = $state<number | null>(null)
  let listEl = $state<HTMLElement | null>(null)

  const VALID_NAME_REGEX = /^[a-zA-Z0-9_.\-]+$/

  function validateModalName(name: string): boolean {
    if (!name.trim()) {
      modalNameError = 'Name is required'
      return false
    }
    if (!VALID_NAME_REGEX.test(name.trim())) {
      modalNameError = 'Only letters, numbers, -, _ and . allowed'
      return false
    }
    modalNameError = ''
    return true
  }

  function handleSave() {
    if (!validateModalName(modalName)) return
    flushPendingLimits()
    onSave(modalName.trim(), models)
  }

  // The limit inputs commit on change (blur/Enter). Clicking Save blurs the
  // field only after the click lands on some browsers, so commit any pending
  // edit here too or a typed-but-not-blurred value is silently dropped.
  function flushPendingLimits() {
    if (!listEl) return
    for (const el of listEl.querySelectorAll<HTMLInputElement>('input[data-model]')) {
      const model = el.dataset.model
      if (!model) continue
      const value = parseLimitInput(el.value)
      if (el.dataset.limit === 'ctx') onSetModelContextLimit(model, value)
      else onSetModelRps(model, value)
    }
  }

  function moveModel(idx: number, delta: number) {
    const arr = [...models]
    const target = idx + delta
    if (target < 0 || target >= arr.length) return
    const temp = arr[idx]
    arr[idx] = arr[target]
    arr[target] = temp
    onUpdateModels(arr)
  }

  function removeModel(idx: number) {
    onUpdateModels(models.filter((_, i) => i !== idx))
    if (editingIdx === idx) editingIdx = null
  }

  // Drag reorder. The grabbed row is removed and reinserted at the drop index
  // (array splice, not a swap) so a move across several positions lands in one
  // gesture instead of walking one slot per row crossed.
  function onDragStart(e: DragEvent, idx: number) {
    dragIdx = idx
    e.dataTransfer?.setData('text/plain', String(idx))
    if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move'
  }

  function onDragOver(e: DragEvent, idx: number) {
    e.preventDefault()
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
    if (overIdx !== idx) overIdx = idx
    autoScrollList(e.clientY)
  }

  function autoScrollList(clientY: number) {
    const el = listEl
    if (!el) return
    const r = el.getBoundingClientRect()
    const edge = 28
    if (clientY < r.top + edge) el.scrollTop -= 12
    else if (clientY > r.bottom - edge) el.scrollTop += 12
  }

  function onDrop(e: DragEvent, idx: number) {
    e.preventDefault()
    const from = dragIdx ?? Number.parseInt(e.dataTransfer?.getData('text/plain') ?? '', 10)
    dragIdx = null
    overIdx = null
    if (Number.isNaN(from) || from === idx || from === null) return
    const arr = [...models]
    const [moved] = arr.splice(from, 1)
    arr.splice(idx, 0, moved)
    onUpdateModels(arr)
  }

  function onDragEnd() {
    dragIdx = null
    overIdx = null
  }

  // Keyboard reorder: the grip handle is focusable and moves the row with the
  // arrow keys, so reordering works without a pointer.
  function onGripKeydown(e: KeyboardEvent, idx: number) {
    if (e.key === 'ArrowUp') {
      e.preventDefault()
      moveModel(idx, -1)
    } else if (e.key === 'ArrowDown') {
      e.preventDefault()
      moveModel(idx, 1)
    }
  }

  function startEdit(idx: number, model: string) {
    editingIdx = idx
    editDraft = model
  }

  function commitEdit(idx: number) {
    const trimmed = editDraft.trim()
    if (trimmed && trimmed !== models[idx]) {
      const arr = [...models]
      arr[idx] = trimmed
      onUpdateModels(arr)
    }
    editingIdx = null
  }
</script>

<Modal
  {isOpen}
  onClose={onClose}
  title={editingCombo ? 'Edit Combo' : 'Create Combo'}
  size="lg"
>
  <div class="flex flex-col gap-3">
    <!-- Name -->
    <div>
      <Input
        label="Combo Name"
        bind:value={modalName}
        placeholder="my-combo"
        error={modalNameError}
      />
      <p class="text-[10px] text-text-muted mt-0.5">
        Only letters, numbers, -, _ and . allowed
      </p>
    </div>

    <!-- Models -->
    <div>
      <label class="text-sm font-medium mb-1.5 block">Models</label>

      {#if models.length === 0}
        <div class="text-center py-4 border border-dashed border-black/10 dark:border-white/10 rounded-lg bg-black/[0.01] dark:bg-white/[0.01]">
          <Layers class="w-6 h-6 text-text-muted mx-auto mb-1 opacity-50" />
          <p class="text-xs text-text-muted">No models added yet</p>
        </div>
      {:else}
        <div
          bind:this={listEl}
          class="flex flex-col gap-1 max-h-[55vh] overflow-y-auto sm:max-h-[350px]"
          role="list"
        >
          {#each models as model, idx (model + '-' + idx)}
            {@const caps = getModelCaps(model)}
            <div
              role="listitem"
              draggable="true"
              ondragstart={(e) => onDragStart(e, idx)}
              ondragover={(e) => onDragOver(e, idx)}
              ondrop={(e) => onDrop(e, idx)}
              ondragend={onDragEnd}
              class="group flex min-w-0 items-center gap-1.5 rounded-md px-2 py-1 bg-black/[0.02] hover:bg-black/[0.04] dark:bg-white/[0.02] dark:hover:bg-white/[0.04] transition-colors {dragIdx === idx
                ? 'opacity-40'
                : ''} {overIdx === idx && dragIdx !== null && dragIdx !== idx
                ? 'ring-1 ring-brand-500'
                : ''}"
            >
              <span
                class="flex h-6 w-4 shrink-0 cursor-grab items-center justify-center text-text-muted active:cursor-grabbing focus-visible:cursor-grab"
                role="button"
                tabindex="0"
                aria-label={`Reorder ${model}. Use arrow up and arrow down keys to move.`}
                onkeydown={(e) => onGripKeydown(e, idx)}
              >
                <GripVertical class="w-3.5 h-3.5" />
              </span>
              <span class="text-[10px] font-medium text-text-muted w-3 text-center shrink-0">{idx + 1}</span>
              {#if editingIdx === idx}
                <input
                  autofocus
                  bind:value={editDraft}
                  onblur={() => commitEdit(idx)}
                  onkeydown={(e) => {
                    if (e.key === 'Enter') commitEdit(idx)
                    if (e.key === 'Escape') editingIdx = null
                  }}
                  class="min-w-0 flex-1 rounded border border-brand-500/40 bg-white px-1.5 py-0.5 font-mono text-xs text-text-main outline-none dark:bg-black/20"
                />
              {:else}
                <div
                  class="min-w-0 flex-1 cursor-text truncate rounded px-1.5 py-0.5 font-mono text-xs text-text-main hover:bg-black/5 dark:hover:bg-white/5"
                  onclick={() => startEdit(idx, model)}
                  onkeydown={(e) => e.key === 'Enter' && startEdit(idx, model)}
                  role="textbox"
                  tabindex="0"
                  title="Click to edit"
                >
                  {model}
                </div>
              {/if}
              {#if caps.vision}
                <Eye class="w-3 h-3 text-blue-500 shrink-0" title="Vision — Supports image input" />
              {/if}
              {#if caps.reasoning}
                <Brain class="w-3 h-3 text-amber-500 shrink-0" title="Reasoning — Supports reasoning / thinking" />
              {/if}
              <label class="flex shrink-0 items-center gap-1" title="Requests per second ceiling. 0 = unlimited. Applies to this model in every combo.">
                <input
                  type="number"
                  min="0"
                  step="1"
                  inputmode="numeric"
                  placeholder="∞"
                  data-model={model}
                  data-limit="rps"
                  value={getModelLimit(modelRps, model) || ''}
                  onchange={(e) => onSetModelRps(model, parseLimitInput(e.currentTarget.value))}
                  aria-label={`Requests per second limit for ${model}`}
                  class="w-14 rounded border border-border bg-surface-2 px-1 py-0.5 text-center text-[10px] text-text-main outline-none focus:border-brand-500 [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                />
                <span class="text-[10px] text-text-muted">rps</span>
              </label>
              <label class="flex shrink-0 items-center gap-1" title="Input context ceiling in tokens. 0 = whatever the provider allows. Applies to this model in every combo — older messages are dropped to fit.">
                <input
                  type="number"
                  min="0"
                  step="any"
                  inputmode="numeric"
                  placeholder="auto"
                  data-model={model}
                  data-limit="ctx"
                  value={getModelLimit(modelContextLimit, model) || ''}
                  onchange={(e) => onSetModelContextLimit(model, parseLimitInput(e.currentTarget.value))}
                  aria-label={`Input context token limit for ${model}`}
                  class="w-16 rounded border border-border bg-surface-2 px-1 py-0.5 text-center text-[10px] text-text-main outline-none focus:border-brand-500 [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                />
                <span class="text-[10px] text-text-muted">ctx</span>
              </label>
              <div class="flex shrink-0 items-center gap-0.5">
                <button
                  type="button"
                  onclick={() => moveModel(idx, -1)}
                  disabled={idx === 0}
                  class="p-0.5 rounded {idx === 0 ? 'text-text-muted/20 cursor-not-allowed' : 'text-text-muted hover:text-brand-500 hover:bg-black/5 dark:hover:bg-white/5 cursor-pointer'}"
                  title="Move up"
                >
                  <ArrowUp class="w-3 h-3" />
                </button>
                <button
                  type="button"
                  onclick={() => moveModel(idx, 1)}
                  disabled={idx === models.length - 1}
                  class="p-0.5 rounded {idx === models.length - 1 ? 'text-text-muted/20 cursor-not-allowed' : 'text-text-muted hover:text-brand-500 hover:bg-black/5 dark:hover:bg-white/5 cursor-pointer'}"
                  title="Move down"
                >
                  <ArrowDown class="w-3 h-3" />
                </button>
              </div>
              <button
                type="button"
                onclick={() => removeModel(idx)}
                class="p-0.5 hover:bg-red-500/10 rounded text-text-muted hover:text-red-500 transition-all cursor-pointer"
                title="Remove"
              >
                <X class="w-3 h-3" />
              </button>
            </div>
          {/each}
        </div>
      {/if}

      <!-- Add Model button -->
      <button
        type="button"
        onclick={onOpenModelPicker}
        class="w-full mt-2 py-2 border border-dashed border-black/10 dark:border-white/10 rounded-lg text-xs text-brand-500 font-medium hover:text-brand-500 hover:border-brand-500/50 transition-colors flex items-center justify-center gap-1 cursor-pointer"
      >
        <Plus class="w-4 h-4" />
        <span>Add Model</span>
      </button>
    </div>

    <!-- Actions -->
    <div class="flex flex-col gap-2 pt-1 sm:flex-row">
      <Button onclick={onClose} variant="ghost" fullWidth size="sm">
        Cancel
      </Button>
      <Button
        onclick={handleSave}
        fullWidth
        size="sm"
        disabled={!modalName.trim() || !!modalNameError || isSaving}
        loading={isSaving}
      >
        {isSaving ? 'Saving...' : editingCombo ? 'Save' : 'Create'}
      </Button>
    </div>
  </div>
</Modal>
