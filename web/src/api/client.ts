// Client for the local daemon API.

// The token lives in sessionStorage and travels in a header.
const tokenKey = 'cdx.token'

// tokenFromHash extracts the token from the URL fragment.
export function tokenFromHash(hash: string): string {
  const idx = hash.indexOf('token=')
  if (idx < 0) {
    return ''
  }
  return hash.slice(idx + 'token='.length).split('&')[0]
}

// injectedToken returns the token the server embedded in the page.
//
// This is the primary source: the server injects it whenever it serves
// index.html, so the token does not depend on the URL fragment and
// survives a tab reload.
function injectedToken(): string {
  const value = (window as { __CDX_TOKEN__?: string }).__CDX_TOKEN__
  return typeof value === 'string' ? value : ''
}

// readToken returns the token in force.
//
// Order: page token, then URL, then the stored one. The daemon issues a
// new token on every start, so the stored one is only the last resort.
export function readToken(): string {
  const injected = injectedToken()
  if (injected) {
    sessionStorage.setItem(tokenKey, injected)
    return injected
  }

  const fresh = tokenFromHash(window.location.hash)
  if (fresh) {
    sessionStorage.setItem(tokenKey, fresh)
    // The fragment must not stay in history or the address bar.
    history.replaceState(null, '', window.location.pathname)
    return fresh
  }
  return sessionStorage.getItem(tokenKey) ?? ''
}

// clearToken forgets the stored token.
export function clearToken() {
  sessionStorage.removeItem(tokenKey)
}

export class ApiError extends Error {
  code: string
  status: number

  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

// request performs an API call.
export async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { 'X-CDX-Token': readToken() }
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
  }

  const res = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  })

  if (!res.ok) {
    let code = 'http_error'
    let message = res.statusText
    try {
      const payload = await res.json()
      code = payload.code ?? code
      message = payload.message ?? message
    } catch {
      // The body is not JSON: the status stands.
    }
    throw new ApiError(res.status, code, message)
  }
  if (res.status === 204) {
    return undefined as T
  }
  return (await res.json()) as T
}

export function getStatus() {
  return request<Status>('GET', '/api/status')
}

export function getConfig() {
  return request<ConfigView>('GET', '/api/config')
}

export function putConfig(cfg: ConfigView) {
  return request<SaveResult>('PUT', '/api/config', cfg)
}

export function getProviders() {
  return request<{ providers: ProviderView[] }>('GET', '/api/providers')
}

// saveProvider creates or updates a provider.
export function saveProvider(edit: ProviderEdit) {
  return request<{ saved: boolean }>('POST', '/api/providers', edit)
}

export function deleteProvider(name: string) {
  return request<void>('DELETE', `/api/providers/${encodeURIComponent(name)}`)
}

export function setProviderKey(name: string, key: string) {
  return request<void>('PUT', `/api/providers/${encodeURIComponent(name)}/key`, { key })
}

export function testProvider(name: string) {
  return request<{ ok: boolean; message: string }>(
    'POST',
    `/api/providers/${encodeURIComponent(name)}/test`,
  )
}

export function discoverModels(name: string) {
  return request<{ models: DiscoveredModelView[] }>(
    'GET',
    `/api/providers/${encodeURIComponent(name)}/models/discover`,
  )
}

// saveModel creates or updates a model.
export function saveModel(edit: ModelEdit) {
  return request<{ saved: boolean }>('POST', '/api/models', edit)
}

export function deleteModel(provider: string, name: string) {
  return request<void>(
    'DELETE',
    `/api/providers/${encodeURIComponent(provider)}/models/${encodeURIComponent(name)}`,
  )
}

export function setDefaultModel(provider: string, name: string) {
  return request<{ saved: boolean }>(
    'PUT',
    `/api/providers/${encodeURIComponent(provider)}/models/${encodeURIComponent(name)}/default`,
  )
}

export function getSessions() {
  return request<{ sessions: SessionView[] }>('GET', '/api/sessions')
}

export function getSession(id: string) {
  return request<{ session: SessionView; messages: MessageView[] }>('GET', `/api/sessions/${id}`)
}

export function getPrompt() {
  return request<PromptView>('GET', '/api/prompt')
}

export function putPrompt(text: string) {
  return request<void>('PUT', '/api/prompt', { text })
}

export function resetPrompt() {
  return request<void>('DELETE', '/api/prompt')
}

export function getLogs(lines = 200) {
  return request<{ lines: string[] }>('GET', `/api/logs?tail=${lines}`)
}

export function getPermissions() {
  return request<PermissionsView>('GET', '/api/permissions')
}

export interface Status {
  state: string
  version: string
  proto: number
  pid: number
  uptime_s: number
  language: string
}

export interface SaveResult {
  saved: boolean
  restart_required: boolean
}

export interface ConfigView {
  agent: {
    model: string
    max_iterations: number
    turn_timeout_ms: number
  }
  context: {
    strategy: string
    recent_messages: number
  }
  permissions: PermissionsView
  output: { color: string; language: string; bell?: boolean }
}

export interface PermissionsView {
  read: string
  edit: string
  dangerous: string
  non_interactive: string
}

export interface ProviderView {
  name: string
  protocol: string
  base_url: string
  has_key: boolean
  models: ModelView[]
}

export interface DiscoveredModelView {
  id: string
  context_window: number
  max_tokens: number
  configured: boolean
  local_name?: string
}

export interface ModelView {
  provider: string
  name: string
  remote: string
  default: boolean
  context_window: number
  max_tokens: number
  temperature: number
  reasoning_levels: string[]
  reasoning: Record<string, string> | null
  reasoning_default: string
  supports_reasoning_effort: boolean
  max_tokens_field: string
  skip_temperature: boolean
}

export interface SessionView {
  id: string
  root: string
  title: string
  model: string
  created_at: number
  updated_at: number
  messages: number
  active: boolean
  inherited: boolean
}

export interface MessageView {
  seq: number
  kind: string
  content: string
  tool_name: string
  model: string
  tokens_in: number
  tokens_out: number
  covers_from: number
  covers_to: number
  ts: number
}

export interface PromptView {
  text: string
  is_default: boolean
}

export interface ProviderEdit {
  original?: string
  name: string
  protocol: string
  base_url: string
  api_key?: string
}

export interface ModelEdit {
  provider: string
  original?: string
  name: string
  remote: string
  context_window: number
  max_tokens: number
  temperature: number
  default: boolean
  reasoning: Record<string, string>
  reasoning_default: string
  supports_reasoning_effort: boolean
  max_tokens_field: string
  skip_temperature: boolean
}

export function getRunning() {
  return request<{ turns: RunningView[] }>('GET', '/api/running')
}

export function cancelRunning(id: string) {
  return request<{ canceled: boolean }>('POST', `/api/running/${encodeURIComponent(id)}/cancel`)
}

export interface RunningView {
  id: string
  model: string
  preset: string
  prompt: string
  cwd: string
  started: number
}

export function getPresets() {
  return request<{ presets: PresetView[] }>('GET', '/api/presets')
}

// savePreset creates or updates a preset.
export function savePreset(edit: PresetEdit) {
  return request<{ saved: boolean }>('POST', '/api/presets', edit)
}

export function deletePreset(id: string) {
  return request<void>('DELETE', `/api/presets/${encodeURIComponent(id)}`)
}

// setActivePreset makes a preset the active one.
export function setActivePreset(id: string) {
  return request<{ active: string }>('PUT', `/api/presets/${encodeURIComponent(id)}/active`)
}

export interface PresetView {
  id: string
  name: string
  description: string
  prompt: string
  builtin: boolean
  active: boolean
}

export interface PresetEdit {
  original?: string
  id: string
  name: string
  description: string
  prompt: string
}

export function getPlugins() {
  return request<{ plugins: PluginView[] }>('GET', '/api/plugins')
}

export function setPlugin(name: string, enabled: boolean) {
  return request<{ saved: boolean }>('PUT', `/api/plugins/${encodeURIComponent(name)}`, { enabled })
}

export function installPlugin(url: string) {
  return request<{ plugin: PluginView }>('POST', '/api/plugins', { url })
}

export function deletePlugin(name: string) {
  return request<void>('DELETE', `/api/plugins/${encodeURIComponent(name)}`)
}

export function getMCP() {
  return request<{ servers: MCPView[] }>('GET', '/api/mcp')
}

// saveMCP creates or updates a server.
export function saveMCP(edit: MCPEdit) {
  return request<{ saved: boolean }>('POST', '/api/mcp', edit)
}

export function deleteMCP(name: string) {
  return request<void>('DELETE', `/api/mcp/${encodeURIComponent(name)}`)
}

export function restartMCP(name: string) {
  return request<{ restarted: boolean }>('POST', `/api/mcp/${encodeURIComponent(name)}/restart`)
}

export function savePermissions(view: PermissionsView) {
  return request<{ saved: boolean }>('PUT', '/api/permissions', view)
}

export function getRules() {
  return request<{ rules: DangerRuleView[] }>('GET', '/api/dangerous-rules')
}

export function saveRules(rules: DangerRuleView[]) {
  return request<{ saved: boolean }>('PUT', '/api/dangerous-rules', { rules })
}

export interface PluginView {
  name: string
  version: string
  description: string
  author: string
  homepage?: string
  permission?: string
  min_cdx_version?: string
  capabilities: string[]
  tools: string[]
  tool_details?: ToolView[]
  loaded: boolean
  enabled: boolean
}

export interface ToolView {
  name: string
  description: string
  permission: string
}

export interface MCPView {
  name: string
  transport: string
  command: string
  args: string[]
  url: string
  enabled: boolean
  timeout_ms: number
  permission: string
  env: Record<string, string>
  cwd: string
  auto_restart: boolean
  connected: boolean
  tools: number
  failed: boolean
}

export interface MCPEdit {
  original?: string
  name: string
  transport: string
  command: string
  args: string[]
  url: string
  enabled: boolean
  timeout_ms: number
  permission: string
  env: Record<string, string>
  cwd: string
  auto_restart: boolean
}

export interface DangerRuleView {
  name: string
  pattern: string
  severity: string
}
