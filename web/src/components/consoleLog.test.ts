import { describe, expect, test } from 'bun:test'
import { filterConsoleLogs, stripAnsi } from './consoleLog'

describe('stripAnsi', () => {
  test('removes SGR color codes', () => {
    expect(stripAnsi('\u001b[32mhello\u001b[0m')).toBe('hello')
  })

  test('leaves plain text untouched', () => {
    expect(stripAnsi('plain line')).toBe('plain line')
  })
})

describe('filterConsoleLogs', () => {
  const logs = ['GET /v1/models 200', 'chat upstream timeout', 'WARN retry scheduled']

  test('empty query returns every line', () => {
    expect(filterConsoleLogs(logs, '')).toEqual(logs)
    expect(filterConsoleLogs(logs, '   ')).toEqual(logs)
  })

  test('matches a substring case-insensitively', () => {
    expect(filterConsoleLogs(logs, 'TIMEOUT')).toEqual(['chat upstream timeout'])
    expect(filterConsoleLogs(logs, 'warn')).toEqual(['WARN retry scheduled'])
  })

  test('trims the query before matching', () => {
    expect(filterConsoleLogs(logs, '  upstream  ')).toEqual(['chat upstream timeout'])
  })

  test('no match yields an empty list', () => {
    expect(filterConsoleLogs(logs, 'zzz')).toEqual([])
  })

  test('matches through ANSI codes', () => {
    expect(filterConsoleLogs(['\u001b[31mERROR bad gateway\u001b[0m'], 'bad gateway')).toEqual([
      '\u001b[31mERROR bad gateway\u001b[0m'
    ])
  })

  test('does not mutate the input', () => {
    const input = [...logs]
    filterConsoleLogs(input, 'timeout')
    expect(input).toEqual(logs)
  })
})
