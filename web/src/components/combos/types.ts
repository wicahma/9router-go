import type { Combo } from '../../api/client'

export interface ComboStrategyInfo {
  fallbackStrategy?: string
  judgeModel?: string
}

export interface AdapterPool {
  enabled: boolean
  roundRobin: boolean
  models: string[]
}

export interface CapacityAdapterState {
  vision: AdapterPool
  audioInput: AdapterPool
}


export function hasVision(model: string): boolean {
  const m = model.toLowerCase()
  return (
    m.includes('vision') ||
    m.includes('gemini') ||
    m.includes('claude-3') ||
    m.includes('claude-sonnet') ||
    m.includes('claude-opus') ||
    m.includes('gpt-4o') ||
    m.includes('spark') ||
    m.includes('vl') ||
    m.includes('flash')
  )
}

export function hasReasoning(model: string): boolean {
  const m = model.toLowerCase()
  return (
    m.includes('reason') ||
    m.includes('think') ||
    m.includes('r1') ||
    m.includes('deepseek') ||
    m.includes('spark') ||
    m.includes('high') ||
    m.includes('o1') ||
    m.includes('o3') ||
    m.includes('pro-agent')
  )
}

export function getComboModels(c: Combo): string[] {
  if (Array.isArray(c.models)) return c.models
  if (typeof c.models === 'string') {
    try {
      const parsed = JSON.parse(c.models)
      if (Array.isArray(parsed)) return parsed
      return [c.models]
    } catch {
      return c.models ? [c.models] : []
    }
  }
  return []
}


export function updateComboStrategy(
  currentStrategies: Record<string, ComboStrategyInfo>,
  comboName: string,
  newStrategy: string
): Record<string, ComboStrategyInfo> {
  const updated = { ...currentStrategies }
  const current = updated[comboName] || {}
  const next = { ...current, fallbackStrategy: newStrategy }

  if (newStrategy === 'fallback' && !next.judgeModel) {
    delete updated[comboName]
  } else {
    updated[comboName] = next
  }
  return updated
}

export function updateJudgeModel(
  currentStrategies: Record<string, ComboStrategyInfo>,
  comboName: string,
  judgeModel: string
): Record<string, ComboStrategyInfo> {
  const updated = { ...currentStrategies }
  const current = updated[comboName] || {}
  updated[comboName] = { ...current, judgeModel }
  return updated
}

export function clearJudgeModel(
  currentStrategies: Record<string, ComboStrategyInfo>,
  comboName: string
): Record<string, ComboStrategyInfo> {
  const updated = { ...currentStrategies }
  if (updated[comboName]) {
    const { judgeModel: _, ...rest } = updated[comboName]
    if (!rest.fallbackStrategy || rest.fallbackStrategy === 'fallback') {
      delete updated[comboName]
    } else {
      updated[comboName] = rest
    }
  }
  return updated
}

// Per-model RPS ceilings live in settings as a plain model -> rps map, shared
// by every combo. 0 (or a missing key) means the model is never throttled, so
// the helpers below treat any non-positive value as "remove the limit" rather
// than storing a zero the gateway would read as a real ceiling.

export function getModelRps(
  limits: Record<string, number> | undefined,
  model: string
): number {
  return limits?.[model] ?? 0
}

export function updateModelRps(
  limits: Record<string, number>,
  model: string,
  rps: number
): Record<string, number> {
  const next = { ...limits }
  if (Number.isFinite(rps) && rps > 0) next[model] = Math.floor(rps)
  else delete next[model]
  return next
}

export function parseRpsInput(value: string): number {
  return Number.parseInt(value, 10)
}

export function normalizeModelRps(raw: unknown): Record<string, number> {
  if (!raw || typeof raw !== 'object') return {}
  const out: Record<string, number> = {}
  for (const [model, value] of Object.entries(raw as Record<string, unknown>)) {
    const nps = Number(value)
    if (Number.isFinite(nps) && nps > 0) out[model] = Math.floor(nps)
  }
  return out
}

export function parseCapacityAdapterSettings(ca: Record<string, unknown> | undefined): CapacityAdapterState {
  const v = ca?.vision as Record<string, unknown> | undefined
  const a = ca?.audioInput as Record<string, unknown> | undefined
  return {
    vision: {
      enabled: v?.enabled !== false,
      roundRobin: !!v?.roundRobin,
      models: Array.isArray(v?.models) ? (v.models as string[]) : ['ag/gemini-3.7-flash-high'],
    },
    audioInput: {
      enabled: a?.enabled !== false,
      roundRobin: !!a?.roundRobin,
      models: Array.isArray(a?.models) ? (a.models as string[]) : [],
    },
  }
}
