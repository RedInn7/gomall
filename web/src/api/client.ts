export type HttpMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'

const ACCESS_KEY = 'gomall_access_token'
const REFRESH_KEY = 'gomall_refresh_token'

export const session = {
  access: () => localStorage.getItem(ACCESS_KEY) || '',
  refresh: () => localStorage.getItem(REFRESH_KEY) || '',
  save(access = '', refresh = '') {
    if (access) localStorage.setItem(ACCESS_KEY, access)
    if (refresh) localStorage.setItem(REFRESH_KEY, refresh)
  },
  clear() {
    localStorage.removeItem(ACCESS_KEY)
    localStorage.removeItem(REFRESH_KEY)
  },
}

export class ApiError extends Error {
  constructor(message: string, public status: number, public payload?: unknown) {
    super(message)
  }
}

const interpolate = (path: string, params: Record<string, unknown>) => {
  const rest = { ...params }
  const resolved = path.replace(/:([a-z_]+)/gi, (_, key) => {
    const value = rest[key]
    delete rest[key]
    return encodeURIComponent(String(value ?? ''))
  })
  return { resolved, rest }
}

export async function api<T = unknown>(
  method: HttpMethod,
  path: string,
  params: Record<string, unknown> = {},
  extraHeaders: Record<string, string> = {},
) {
  const { resolved, rest } = interpolate(path, params)
  const headers: Record<string, string> = { Accept: 'application/json', ...extraHeaders }
  const access = session.access()
  const refresh = session.refresh()
  if (access) headers.access_token = access
  if (refresh) headers.refresh_token = refresh

  let url = resolved
  let body: string | undefined
  if (method === 'GET') {
    const query = new URLSearchParams()
    Object.entries(rest).forEach(([key, value]) => {
      if (value !== '' && value != null) query.set(key, String(value))
    })
    if ([...query].length) url += `?${query}`
  } else {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(rest)
  }

  const response = await fetch(url, { method, headers, body })
  const payload = await response.json().catch(() => null)
  if (!response.ok) throw new ApiError(`HTTP ${response.status}`, response.status, payload)
  const envelope = payload as any
  const businessStatus = envelope?.status ?? envelope?.code
  if (businessStatus != null && Number(businessStatus) !== 0 && Number(businessStatus) !== 200) {
    throw new ApiError(envelope.msg || envelope.message || envelope.error || '请求失败', response.status, payload)
  }
  return (envelope?.data ?? payload) as T
}

export async function apiForm<T = unknown>(path: string, params: Record<string, unknown>, file?: File) {
  const headers: Record<string, string> = {}
  if (session.access()) headers.access_token = session.access()
  if (session.refresh()) headers.refresh_token = session.refresh()
  const body = new FormData()
  Object.entries(params).forEach(([key, value]) => body.append(key, String(value ?? '')))
  if (file) body.append(path.endsWith('/avatar') ? 'file' : 'image', file)
  const response = await fetch(path, { method: 'POST', headers, body })
  const payload = await response.json().catch(() => null)
  const envelope = payload as any
  if (!response.ok || (envelope?.status != null && Number(envelope.status) !== 200)) {
    throw new ApiError(envelope?.msg || envelope?.error || `HTTP ${response.status}`, response.status, payload)
  }
  return (envelope?.data ?? payload) as T
}

export function captureTokens(data: any) {
  const access = data?.access_token ?? data?.AccessToken
  const refresh = data?.refresh_token ?? data?.RefreshToken
  if (access || refresh) session.save(access, refresh)
}
