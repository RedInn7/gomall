import { useMemo, useState } from 'react'
import { api, apiForm, captureTokens, session } from '../api/client'
import { ENDPOINTS, GROUPS, type Endpoint } from '../api/endpoints'
import { cx } from '../lib/util'

const pretty = (value: unknown) => JSON.stringify(value, null, 2)

const IDEMPOTENT_PATHS = new Set([
  '/api/v1/orders/create',
  '/api/v1/orders/enqueue',
  '/api/v1/paydown',
  '/api/v1/paydown/crypto',
  '/api/v1/paydown/stripe',
  '/api/v1/paydown/xcash',
  '/api/v1/orders/refund/request',
  '/api/v1/redpacket/create',
  '/api/v1/redpacket/claim',
])

function Action({ endpoint, onAuthChange }: { endpoint: Endpoint; onAuthChange: () => void }) {
  const [input, setInput] = useState(pretty(endpoint.sample ?? {}))
  const [result, setResult] = useState('')
  const [busy, setBusy] = useState(false)
  const [file, setFile] = useState<File>()
  const needsFile = endpoint.path === '/api/v1/user/avatar' || endpoint.path === '/api/v1/product/create'

  const run = async () => {
    setBusy(true)
    try {
      const params = input.trim() ? JSON.parse(input) : {}
      let headers: Record<string, string> = {}
      if (endpoint.method === 'POST' && IDEMPOTENT_PATHS.has(endpoint.path)) {
        const token = await api<{ idempotency_key: string }>('GET', '/api/v1/idempotency/token')
        headers = { 'Idempotency-Key': token.idempotency_key }
      }
      const data = needsFile ? await apiForm(endpoint.path, params, file) : await api(endpoint.method, endpoint.path, params, headers)
      if (endpoint.path === '/api/v1/user/login' || endpoint.path === '/api/v1/user/register') {
        captureTokens(data)
        onAuthChange()
      }
      setResult(pretty(data))
      if (endpoint.method === 'POST' && (endpoint.path === '/api/v1/paydown/stripe' || endpoint.path === '/api/v1/paydown/xcash') && (data as any)?.url) {
        window.open((data as any).url, '_blank', 'noopener,noreferrer')
      }
    } catch (error: any) {
      setResult(pretty(error?.payload ?? { error: error?.message || '请求失败' }))
    } finally {
      setBusy(false)
    }
  }

  return (
    <article className="wb-action">
      <header>
        <div><span className={`wb-method wb-method--${endpoint.method.toLowerCase()}`}>{endpoint.method}</span><b>{endpoint.label}</b></div>
        <code>{endpoint.path}</code>
      </header>
      <textarea value={input} onChange={(event) => setInput(event.target.value)} spellCheck={false} aria-label={`${endpoint.label} 请求参数`} />
      {needsFile && <label className="wb-file">选择图片<input type="file" accept="image/*" onChange={(event) => setFile(event.target.files?.[0])} /></label>}
      <button className="btn btn--gold" disabled={busy} onClick={run}>{busy ? '处理中…' : '执行'}</button>
      {result && <pre>{result}</pre>}
    </article>
  )
}

export function Workbench({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [group, setGroup] = useState(GROUPS[0])
  const [authVersion, setAuthVersion] = useState(0)
  const endpoints = useMemo(() => ENDPOINTS.filter((item) => item.group === group), [group])
  const loggedIn = Boolean(session.access())

  return (
    <section className={cx('workbench', open && 'show')} aria-hidden={!open}>
      <header className="workbench__head">
        <div>
          <p className="kicker">GOMALL CONTROL ROOM</p>
          <h2>业务工作台</h2>
          <p>从用户下单到商家履约，每个操作都直接调用 GoMall 后端。</p>
        </div>
        <div className="workbench__session" key={authVersion}>
          <span className={loggedIn ? 'is-online' : ''}>{loggedIn ? '已登录' : '未登录'}</span>
          {loggedIn && <button onClick={() => { session.clear(); setAuthVersion((v) => v + 1) }}>退出</button>}
          <button className="workbench__close" onClick={onClose}>关闭 ✕</button>
        </div>
      </header>
      <div className="workbench__layout">
        <nav className="workbench__nav" aria-label="业务模块">
          {GROUPS.map((name) => <button key={name} className={group === name ? 'is-on' : ''} onClick={() => setGroup(name)}>{name}<i>{ENDPOINTS.filter((item) => item.group === name).length}</i></button>)}
        </nav>
        <main className="workbench__main">
          <div className="workbench__intro"><span>{group}</span><p>参数使用 JSON 填写；登录成功后令牌会自动保存，后续请求会自动携带。</p></div>
          <div className="workbench__grid">
            {endpoints.map((endpoint) => <Action key={`${endpoint.method}-${endpoint.path}`} endpoint={endpoint} onAuthChange={() => setAuthVersion((v) => v + 1)} />)}
          </div>
        </main>
      </div>
    </section>
  )
}
