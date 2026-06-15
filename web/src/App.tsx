import { useCallback, useEffect, useRef, useState } from 'react'
import type { Cat, Product } from './types'
import { PRODUCTS } from './data/products'
import { useCart } from './hooks/useCart'
import { Cursor, Grain, Loader, Ticker } from './components/Atmosphere'
import { Nav } from './components/Nav'
import { Hero } from './components/Hero'
import { Collection, ValueStrip } from './components/Collection'
import { Editorial, Footer, Promise as PromiseSec, Signup } from './components/Sections'
import { CartDrawer, ProductDetail, SearchOverlay } from './components/Overlays'

type Overlay = 'search' | 'cart' | 'detail' | null

export default function App() {
  const [items, setItems] = useState<Product[]>(PRODUCTS)
  const findP = useCallback((id: number) => items.find((p) => p.id === id) ?? PRODUCTS.find((p) => p.id === id), [items])
  const cart = useCart(findP)

  const [overlay, setOverlay] = useState<Overlay>(null)
  const [detail, setDetail] = useState<Product | null>(null)

  /* toast */
  const [toastMsg, setToastMsg] = useState('')
  const [toastOn, setToastOn] = useState(false)
  const toastT = useRef<number>()
  const toast = useCallback((m: string) => {
    setToastMsg(m); setToastOn(true)
    clearTimeout(toastT.current)
    toastT.current = window.setTimeout(() => setToastOn(false), 2600)
  }, [])

  /* overlay helpers */
  const close = useCallback(() => setOverlay(null), [])
  useEffect(() => { document.body.style.overflow = overlay ? 'hidden' : '' }, [overlay])
  useEffect(() => {
    const fn = (e: KeyboardEvent) => { if (e.key === 'Escape') close() }
    addEventListener('keydown', fn); return () => removeEventListener('keydown', fn)
  }, [close])

  const nav = useCallback((hash: string) => {
    close()
    const t = document.querySelector(hash.length > 1 ? hash : '#top')
    t?.scrollIntoView({ behavior: 'smooth' })
  }, [close])

  const addToCart = useCallback((id: number, qty = 1, size = 'Standard') => {
    const p = findP(id); if (!p || p.sold) return
    cart.add(id, qty, size)
    toast(`${p.name} · 已入袋`)
  }, [findP, cart, toast])

  const openDetail = useCallback((id: number) => {
    const p = findP(id); if (!p) return
    setDetail(p); setOverlay('detail')
  }, [findP])

  const addFromDetail = useCallback((id: number, qty: number, size: string) => {
    addToCart(id, qty, size)
    setOverlay('cart')
  }, [addToCart])

  const checkout = useCallback(() => {
    if (!cart.count) return
    toast('结算已起 · 谢谢你的耐心 ✦')
    cart.clear()
    setTimeout(close, 700)
  }, [cart, toast, close])

  /* 可选：融合后端 /api/v1/products，接不到则保留 mock 典藏 */
  useEffect(() => {
    const ctrl = new AbortController()
    const to = setTimeout(() => ctrl.abort(), 1500)
    fetch('/api/v1/products', { signal: ctrl.signal })
      .then((r) => (r.ok ? r.json() : null))
      .then((body) => {
        const raw = body?.data?.item ?? body?.data?.list ?? body?.data ?? []
        if (!Array.isArray(raw) || !raw.length) return
        const cats: Cat[] = ['objet', 'atelier', 'edition', 'voyage']
        const mapped: Product[] = raw.slice(0, 12).map((r: any, i: number) => ({
          id: 1000 + (r.id || i),
          name: r.title || r.name || `Objet ${i}`,
          cat: cats[i % cats.length],
          price: Math.round(Number(r.price) || 1980),
          off: r.discount_price ? Math.round(Number(r.discount_price)) : 0,
          img: PRODUCTS[i % PRODUCTS.length].img,
          span: i % 4 === 0 ? 'wide' : '',
          desc: r.info || r.title || '来自工坊的造物。',
        }))
        if (mapped.length) setItems(mapped)
      })
      .catch(() => { /* offline / 无后端：保留 seed 典藏 */ })
      .finally(() => clearTimeout(to))
    return () => { clearTimeout(to); ctrl.abort() }
  }, [])

  return (
    <>
      <Grain />
      <Cursor />
      <Loader />
      <Ticker />
      <Nav cartCount={cart.count} onSearch={() => setOverlay('search')} onCart={() => setOverlay('cart')} onNav={nav} />

      <main>
        <Hero count={items.length} onNav={nav} />
        <ValueStrip />
        <Collection items={items} onOpen={openDetail} onAdd={addToCart} />
        <Editorial onNav={nav} />
        <PromiseSec />
        <Signup toast={toast} />
      </main>

      <Footer onNav={nav} toast={toast} />

      <SearchOverlay open={overlay === 'search'} items={items} onClose={close}
        onOpenProduct={(id) => openDetail(id)} />
      <ProductDetail product={detail} open={overlay === 'detail'} onClose={close} onAdd={addFromDetail} />
      <CartDrawer open={overlay === 'cart'} lines={cart.lines} findP={findP} total={cart.total} count={cart.count}
        onClose={close} setQty={cart.setQty} onCheckout={checkout} onBrowse={() => nav('#collection')} />

      <div className={`scrim${overlay ? ' show' : ''}`} onClick={close} aria-hidden />
      <div className={`toast${toastOn ? ' show' : ''}`} aria-live="polite">{toastMsg}</div>
    </>
  )
}
