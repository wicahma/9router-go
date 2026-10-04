import { describe, expect, it } from 'vitest'
import {
  COMBO_STRATEGIES,
  getModelLimit,
  normalizeModelLimits,
  resolveComboStrategy,
  updateModelLimit
} from './types'

const OPTION_VALUES = COMBO_STRATEGIES.map((s) => s.value)

// One set of helpers backs both settings keys (modelRps, modelContextLimit),
// so the cases below are written against the shape rather than either name.
describe('per-model limits', () => {
  it('defaults to uncapped', () => {
    expect(getModelLimit(undefined, 'openai/gpt-4o')).toBe(0)
    expect(getModelLimit({}, 'openai/gpt-4o')).toBe(0)
  })

  it('reads a configured limit', () => {
    expect(getModelLimit({ 'openai/gpt-4o': 10 }, 'openai/gpt-4o')).toBe(10)
  })

  it('sets a limit', () => {
    expect(updateModelLimit({}, 'openai/gpt-4o', 10)).toEqual({ 'openai/gpt-4o': 10 })
  })

  it('replaces an existing limit', () => {
    expect(updateModelLimit({ 'openai/gpt-4o': 10 }, 'openai/gpt-4o', 20)).toEqual({
      'openai/gpt-4o': 20,
    })
  })

  it('removes the key when set to zero or below', () => {
    expect(updateModelLimit({ 'openai/gpt-4o': 10 }, 'openai/gpt-4o', 0)).toEqual({})
    expect(updateModelLimit({ 'openai/gpt-4o': 10 }, 'openai/gpt-4o', -5)).toEqual({})
  })

  it('does not mutate the input', () => {
    const before = { 'openai/gpt-4o': 10 }
    updateModelLimit(before, 'openai/gpt-4o', 20)
    expect(before).toEqual({ 'openai/gpt-4o': 10 })
  })

  it('floors fractional input and drops non-numbers', () => {
    expect(updateModelLimit({}, 'a', 12.9)).toEqual({ a: 12 })
    expect(updateModelLimit({}, 'a', Number.NaN)).toEqual({})
    expect(updateModelLimit({}, 'a', Number.POSITIVE_INFINITY)).toEqual({})
  })

  it('keeps a token-scale context value intact', () => {
    const ctx = updateModelLimit({}, 'custom/local-llama', 200_000)
    expect(ctx).toEqual({ 'custom/local-llama': 200_000 })
    expect(getModelLimit(ctx, 'custom/local-llama')).toBe(200_000)
  })

  it('normalises a settings payload from the server', () => {
    expect(normalizeModelLimits({ 'a': 10, 'b': 0, 'c': -1, 'd': 'x' })).toEqual({ a: 10 })
    expect(normalizeModelLimits(undefined)).toEqual({})
    expect(normalizeModelLimits('nope')).toEqual({})
  })
})

describe('resolveComboStrategy', () => {
  // The server fills combo.strategy from settings.comboStrategies[name] and
  // otherwise from the global `comboStrategy`, so a card can receive either
  // vocabulary. Anything it can receive has to render as one of the options.
  it('maps every value the server can send onto an existing option', () => {
    const serverValues = [
      undefined,
      null,
      '',
      'fallback',
      'round-robin',
      'first-model',
      'sticky',
      'capacity',
      'fusion',
      'nonsense-from-a-hand-edited-backup'
    ]

    for (const value of serverValues) {
      expect(OPTION_VALUES).toContain(resolveComboStrategy(value))
    }
  })

  it('treats the global routing mode as the card fallback', () => {
    // `first-model` is the global Combo Routing Mode and the default when a
    // combo has no per-combo entry. It is try-in-order, which is exactly what
    // the card calls `fallback`.
    expect(resolveComboStrategy('first-model')).toBe('fallback')
  })

  it('passes a real per-combo strategy through unchanged', () => {
    for (const value of ['round-robin', 'sticky', 'capacity', 'fusion']) {
      expect(resolveComboStrategy(value)).toBe(value)
    }
  })

  it('falls back for a missing or unrecognised value', () => {
    expect(resolveComboStrategy(undefined)).toBe('fallback')
    expect(resolveComboStrategy(null)).toBe('fallback')
    expect(resolveComboStrategy('')).toBe('fallback')
    expect(resolveComboStrategy('nonsense')).toBe('fallback')
  })

  it('lists every option with a non-empty label', () => {
    for (const strategy of COMBO_STRATEGIES) {
      expect(strategy.value.length).toBeGreaterThan(0)
      expect(strategy.label.length).toBeGreaterThan(0)
    }
  })
})
