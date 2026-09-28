import { describe, expect, it } from 'vitest'
import { getModelRps, normalizeModelRps, updateModelRps } from './types'

describe('model rps limits', () => {
  it('defaults to unlimited', () => {
    expect(getModelRps(undefined, 'openai/gpt-4o')).toBe(0)
    expect(getModelRps({}, 'openai/gpt-4o')).toBe(0)
  })

  it('reads a configured limit', () => {
    expect(getModelRps({ 'openai/gpt-4o': 10 }, 'openai/gpt-4o')).toBe(10)
  })

  it('sets a limit', () => {
    expect(updateModelRps({}, 'openai/gpt-4o', 10)).toEqual({ 'openai/gpt-4o': 10 })
  })

  it('replaces an existing limit', () => {
    expect(updateModelRps({ 'openai/gpt-4o': 10 }, 'openai/gpt-4o', 20)).toEqual({
      'openai/gpt-4o': 20,
    })
  })

  it('removes the key when set to zero or below', () => {
    expect(updateModelRps({ 'openai/gpt-4o': 10 }, 'openai/gpt-4o', 0)).toEqual({})
    expect(updateModelRps({ 'openai/gpt-4o': 10 }, 'openai/gpt-4o', -5)).toEqual({})
  })

  it('does not mutate the input', () => {
    const before = { 'openai/gpt-4o': 10 }
    updateModelRps(before, 'openai/gpt-4o', 20)
    expect(before).toEqual({ 'openai/gpt-4o': 10 })
  })

  it('floors fractional input and drops non-numbers', () => {
    expect(updateModelRps({}, 'a', 12.9)).toEqual({ a: 12 })
    expect(updateModelRps({}, 'a', Number.NaN)).toEqual({})
    expect(updateModelRps({}, 'a', Number.POSITIVE_INFINITY)).toEqual({})
  })

  it('normalises a settings payload from the server', () => {
    expect(normalizeModelRps({ 'a': 10, 'b': 0, 'c': -1, 'd': 'x' })).toEqual({ a: 10 })
    expect(normalizeModelRps(undefined)).toEqual({})
    expect(normalizeModelRps('nope')).toEqual({})
  })
})
