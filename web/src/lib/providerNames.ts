import { api } from '../api/client'

let cache: Record<string, string> | null = null
let inflight: Promise<Record<string, string>> | null = null

// Usage/error payloads carry a raw providerNodes.id ("openai-compatible-chat-<uuid>"),
// which is unreadable in the UI. Resolve it to the node's display name once per
// session; the promise is shared so N components cause one request.
export function loadProviderNames(): Promise<Record<string, string>> {
  if (cache) return Promise.resolve(cache)
  if (!inflight) {
    inflight = api
      .getProviderNodes()
      .then((nodes) => (cache = Object.fromEntries(nodes.map((n) => [n.id, n.name]))))
      .catch(() => (cache = {}))
      .finally(() => {
        inflight = null
      })
  }
  return inflight
}

export function resolveProviderName(id: string | undefined, names: Record<string, string>): string {
  if (!id) return '—'
  return names[id] || shortenProviderId(id)
}

function shortenProviderId(p: string): string {
  const uuid = p.match(/[0-9a-f]{8}-[0-9a-f]{4}/)
  if (!uuid) return p
  const kind = p.split('-chat')[0].split('-')[0]
  return `${kind}…${p.slice(-4)}`
}
