import { useMemo, useState } from 'react'
import type { Cat, Product } from '../types'
import { CAT_LABEL } from '../types'
import { asset, cx, no3, yuan } from '../lib/util'
import { useRevealAll } from '../hooks/useReveal'

const FILTERS: { cat: Cat | 'all'; label: string }[] = [
  { cat: 'all', label: '全部 ALL' },
  { cat: 'objet', label: '器物 OBJETS' },
  { cat: 'atelier', label: '手作 ATELIER' },
  { cat: 'edition', label: '特别版 ÉDITION' },
  { cat: 'voyage', label: '旅程 VOYAGE' },
]

function Card({ p, i, onOpen, onAdd }: { p: Product; i: number; onOpen: (id: number) => void; onAdd: (id: number) => void }) {
  const price = p.off
    ? <><s>{yuan(p.price)}</s>{yuan(p.off)}</>
    : yuan(p.price)
  return (
    <article
      className={cx('card', p.span === 'wide' && 'card--wide', p.span === 'tall' && 'card--tall')}
      data-cat={p.cat}
      style={{ gridColumn: p.span === 'wide' ? 'span 6' : undefined, transitionDelay: `${(i % 6) * 70}ms` }}
    >
      <div className="card__media" data-open onClick={() => onOpen(p.id)}>
        <img className="card__img" src={asset(p.img)} alt={p.name} loading="lazy" />
        <span className="card__tint" />
        <span className="card__no">{no3(p.id)}</span>
        <span className="card__tag">{CAT_LABEL[p.cat].split(' ')[1]}</span>
        {p.sold && <div className="card__sold"><span>已封存 · ARCHIVED</span></div>}
        {!p.sold && (
          <div className="card__quick">
            <button className="q-view" data-open onClick={(e) => { e.stopPropagation(); onOpen(p.id) }}>细览</button>
            <button data-add onClick={(e) => { e.stopPropagation(); onAdd(p.id) }}>加入袋中</button>
          </div>
        )}
      </div>
      <div className="card__meta">
        <div>
          <h3 className="card__name">{p.name}</h3>
          <p className="card__cat">{CAT_LABEL[p.cat]}</p>
        </div>
        <p className="card__price">{price}</p>
      </div>
    </article>
  )
}

export function Collection({ items, onOpen, onAdd }: { items: Product[]; onOpen: (id: number) => void; onAdd: (id: number) => void }) {
  const [cat, setCat] = useState<Cat | 'all'>('all')
  const list = useMemo(() => (cat === 'all' ? items : items.filter((p) => p.cat === cat)), [items, cat])
  const gridRef = useRevealAll<HTMLDivElement>([cat, items])

  return (
    <section className="collection" id="collection">
      <header className="sec-head">
        <div>
          <p className="kicker">典藏 — THE APOGEE COLLECTION</p>
          <h2 className="sec-title">本季造物<em>列陈</em></h2>
        </div>
        <p className="sec-note">以星轨为名的限量系列。每件造物独立编号，售罄即封存。</p>
      </header>

      <div className="filters">
        {FILTERS.map((f) => (
          <button key={f.cat} className={cx('filters__btn', cat === f.cat && 'is-active')} onClick={() => setCat(f.cat)}>
            {f.label}
          </button>
        ))}
      </div>

      <div className="grid" ref={gridRef} key={cat}>
        {list.map((p, i) => <Card key={p.id} p={p} i={i} onOpen={onOpen} onAdd={onAdd} />)}
      </div>
    </section>
  )
}

export function ValueStrip() {
  const words = ['CRAFT', 'ORBIT', 'RARITY', 'PATIENCE']
  const seq = [...words, ...words, ...words, ...words]
  return (
    <section className="strip" aria-hidden>
      <div className="strip__track">
        {seq.map((w, i) => <span key={i} style={{ display: 'contents' }}><span>{w}</span><b>✦</b></span>)}
      </div>
    </section>
  )
}
