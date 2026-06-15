import { useEffect, useMemo, useState } from 'react'
import type { CartLine, Product } from '../types'
import { CAT_LABEL } from '../types'
import { asset, cx, EDITIONS, priceOf, yuan } from '../lib/util'

/* ---------------- Search ---------------- */
export function SearchOverlay({ open, items, onClose, onOpenProduct }: {
  open: boolean; items: Product[]; onClose: () => void; onOpenProduct: (id: number) => void
}) {
  const [q, setQ] = useState('')
  useEffect(() => { if (open) setQ('') }, [open])
  const list = useMemo(() => {
    const s = q.trim().toLowerCase()
    if (!s) return items.slice(0, 6)
    return items.filter((p) => (p.name + ' ' + p.cat + ' ' + CAT_LABEL[p.cat] + ' ' + p.desc).toLowerCase().includes(s))
  }, [q, items])
  const hint = !q.trim()
    ? '试试 “星轨”、“édition” 或 “voyage”。'
    : list.length ? `${list.length} 件造物与「${q}」相遇` : `未寻得「${q}」，换个词试试。`

  return (
    <div className={cx('search', open && 'show')} aria-hidden={!open}>
      <div className="search__inner">
        <div className="search__bar">
          <svg viewBox="0 0 24 24"><circle cx="11" cy="11" r="7" /><line x1="16.5" y1="16.5" x2="21" y2="21" /></svg>
          <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜寻造物 / search the collection…" aria-label="搜索" autoFocus={open} />
          <button onClick={onClose} aria-label="关闭">关闭 ✕</button>
        </div>
        <p className="search__hint">{hint}</p>
        <div className="search__results">
          {list.map((p) => (
            <button key={p.id} className="s-res" onClick={() => onOpenProduct(p.id)}>
              <img src={asset(p.img)} alt="" />
              <span><b>{p.name}</b><span>{yuan(priceOf(p))}</span></span>
            </button>
          ))}
        </div>
      </div>
    </div>
  )
}

/* ---------------- Product detail ---------------- */
export function ProductDetail({ product, open, onClose, onAdd }: {
  product: Product | null; open: boolean; onClose: () => void; onAdd: (id: number, qty: number, size: string) => void
}) {
  const [qty, setQty] = useState(1)
  const [size, setSize] = useState<string>(EDITIONS[0])
  useEffect(() => { if (open) { setQty(1); setSize(EDITIONS[0]) } }, [open, product?.id])
  const p = product
  return (
    <div className={cx('detail', open && 'show')} aria-hidden={!open}>
      <div className="detail__panel">
        <button className="detail__close" onClick={onClose} aria-label="关闭">✕</button>
        <div className="detail__media" data-cat={p?.cat}>
          <div className="detail__imgwrap">{p && <img src={asset(p.img)} alt={p.name} />}</div>
          <span className="detail__no">{p && <>N<sup>o</sup> {String(p.id).padStart(3, '0')} / 100</>}</span>
        </div>
        <div className="detail__info">
          <p className="kicker">{p && CAT_LABEL[p.cat]}</p>
          <h3 className="detail__name">{p?.name}</h3>
          <p className="detail__price">{p && (p.off ? <><s>{yuan(p.price)}</s>{yuan(p.off)}</> : yuan(p.price))}</p>
          <p className="detail__desc">{p?.desc}</p>
          <div>
            <span className="detail__label">规格 / EDITION</span>
            <div className="detail__sizes">
              {EDITIONS.map((s) => (
                <button key={s} className={cx(size === s && 'is-on')} onClick={() => setSize(s)}>{s}</button>
              ))}
            </div>
          </div>
          <div className="detail__buy">
            <div className="qty">
              <button onClick={() => setQty((q) => Math.max(1, q - 1))}>−</button>
              <b>{qty}</b>
              <button onClick={() => setQty((q) => q + 1)}>+</button>
            </div>
            <button
              className="btn btn--gold detail__add"
              disabled={!!p?.sold}
              style={{ opacity: p?.sold ? 0.4 : 1 }}
              onClick={() => { if (p && !p.sold) onAdd(p.id, qty, size) }}
            >
              {p?.sold ? '已封存 · ARCHIVED' : <>加入购物袋 <span>→</span></>}
            </button>
          </div>
          <ul className="detail__meta">
            <li>限量编号 · 独立确权上链</li>
            <li>24 城臻享配送 · 运费由工坊承担</li>
            <li>终身养护 · 可退可换</li>
          </ul>
        </div>
      </div>
    </div>
  )
}

/* ---------------- Cart drawer ---------------- */
export function CartDrawer({ open, lines, findP, total, count, onClose, setQty, onCheckout, onBrowse }: {
  open: boolean; lines: CartLine[]; findP: (id: number) => Product | undefined
  total: number; count: number; onClose: () => void
  setQty: (key: string, qty: number) => void; onCheckout: () => void; onBrowse: () => void
}) {
  const empty = lines.length === 0
  return (
    <aside className={cx('cart', open && 'show')} aria-hidden={!open}>
      <header className="cart__head">
        <h3>购物袋 <span>({count})</span></h3>
        <button onClick={onClose} aria-label="关闭">✕</button>
      </header>

      {empty ? (
        <div className="cart__empty">
          <p>你的购物袋尚是星空。</p>
          <button className="btn btn--ghost" onClick={onBrowse}>去看看典藏</button>
        </div>
      ) : (
        <>
          <div className="cart__body">
            {lines.map((l) => {
              const p = findP(l.id); if (!p) return null
              return (
                <div className="cart-line" key={l.key}>
                  <img className="cart-line__img" src={asset(p.img)} alt={p.name} />
                  <div>
                    <p className="cart-line__name">{p.name}</p>
                    <p className="cart-line__cat">{CAT_LABEL[p.cat]} · {l.size}</p>
                    <span className="cart-line__qty">
                      <button onClick={() => setQty(l.key, l.qty - 1)}>−</button>
                      <b>{l.qty}</b>
                      <button onClick={() => setQty(l.key, l.qty + 1)}>+</button>
                    </span>
                  </div>
                  <div className="cart-line__right">
                    <span className="cart-line__price">{yuan(priceOf(p) * l.qty)}</span>
                    <button className="cart-line__rm" onClick={() => setQty(l.key, 0)}>移除</button>
                  </div>
                </div>
              )
            })}
          </div>
          <footer className="cart__foot">
            <div className="cart__sum"><span>小计 SUBTOTAL</span><b>{yuan(total)}</b></div>
            <p className="cart__note">税费与臻享配送于结算时确认。</p>
            <button className="btn btn--gold cart__checkout" onClick={onCheckout}>结算 CHECKOUT <span>→</span></button>
          </footer>
        </>
      )}
    </aside>
  )
}
