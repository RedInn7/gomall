import { useCallback, useEffect, useRef, useState } from 'react'
import type { Product } from './types'
import { PRODUCTS } from './data/products'
import { useCart } from './hooks/useCart'
import { Cursor, Grain, Loader, Ticker } from './components/Atmosphere'
import { Nav } from './components/Nav'
import { Hero } from './components/Hero'
import { Collection, ValueStrip } from './components/Collection'
import { Editorial, Footer, Promise as PromiseSec, Signup } from './components/Sections'
import { CartDrawer, ProductDetail, SearchOverlay } from './components/Overlays'
import { Workbench } from './components/Workbench'
import { api, session } from './api/client'
import { getProduct, listProducts } from './api/products'

type Overlay = 'search' | 'cart' | 'detail' | 'workbench' | null

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
    if (session.access()) {
      api('POST', '/api/v1/carts/create', { product_id: id }).catch((error) => toast(error?.message || '购物车同步失败'))
    }
    toast(`${p.name} · 已入袋`)
  }, [findP, cart, toast])

  const openDetail = useCallback((id: number) => {
    const p = findP(id); if (!p) return
    setDetail(p); setOverlay('detail')
    getProduct(id).then(setDetail).catch(() => { /* 详情接口暂时不可用时保留列表数据 */ })
  }, [findP])

  const addFromDetail = useCallback((id: number, qty: number, size: string) => {
    addToCart(id, qty, size)
    setOverlay('cart')
  }, [addToCart])

  const checkout = useCallback(() => {
    if (!cart.count) return
    setOverlay('workbench')
    toast(session.access() ? '请在订单模块选择地址并创建订单' : '请先登录，再创建订单')
  }, [cart.count, toast])

  /* 启动后读取后端商品；本地种子保证页面在服务启动前仍可预览。 */
  useEffect(() => {
    let active = true
    listProducts()
      .then((mapped) => { if (active && mapped.length) setItems(mapped) })
      .catch(() => { /* offline / 无后端：保留 seed 典藏 */ })
    return () => { active = false }
  }, [])

  return (
    <>
      <Grain />
      <Cursor />
      <Loader />
      <Ticker />
      <Nav cartCount={cart.count} onSearch={() => setOverlay('search')} onCart={() => setOverlay('cart')} onAccount={() => setOverlay('workbench')} onNav={nav} />

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
      <Workbench open={overlay === 'workbench'} onClose={close} />

      <div className={`scrim${overlay ? ' show' : ''}`} onClick={close} aria-hidden />
      <div className={`toast${toastOn ? ' show' : ''}`} aria-live="polite">{toastMsg}</div>
    </>
  )
}
