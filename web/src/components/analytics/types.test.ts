import { describe, expect, it } from 'bun:test'
import { cachedTokensFor, fmt, fmtMs, type RequestDetailItem } from './types'

describe('request detail token formatting', () => {
  it('prefers canonical cached_tokens', () => {
    const detail: RequestDetailItem = {
      tokens: { cached_tokens: 120, cache_read_input_tokens: 90 },
    }

    expect(cachedTokensFor(detail)).toBe(120)
    expect(fmt(cachedTokensFor(detail))).toBe('120')
  })

  it('falls back to legacy cache_read_input_tokens', () => {
    expect(cachedTokensFor({ tokens: { cache_read_input_tokens: 80 } })).toBe(80)
  })

  it('renders missing cache usage as zero', () => {
    expect(fmt(cachedTokensFor({}))).toBe('0')
    expect(fmt(cachedTokensFor({ tokens: { cached_tokens: 0, cache_read_input_tokens: 25 } }))).toBe('0')
  })
})

describe('latency formatting', () => {
  it('keeps sub-second values in milliseconds', () => {
    expect(fmtMs(0)).toBe('0ms')
    expect(fmtMs(474)).toBe('474ms')
    expect(fmtMs(999)).toBe('999ms')
  })

  it('switches to seconds with a sensible precision', () => {
    expect(fmtMs(1000)).toBe('1.0s')
    expect(fmtMs(17071)).toBe('17s')
    expect(fmtMs(59999)).toBe('60s')
  })

  it('spells out minutes for long requests', () => {
    expect(fmtMs(60000)).toBe('1m 0s')
    expect(fmtMs(147321)).toBe('2m 27s')
    expect(fmtMs(304160)).toBe('5m 4s')
  })

  // A model with no recorded durations must read as "unknown", not "0ms" —
  // zero would claim the request was instant.
  it('renders missing values as unknown', () => {
    expect(fmtMs(undefined)).toBe('—')
    expect(fmtMs(null as unknown as number)).toBe('—')
  })
})
