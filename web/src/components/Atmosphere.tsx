import { useEffect, useRef, useState } from 'react'

/** 香槟金自定义光标：阻尼跟随 + 悬停交互元素时放大。 */
export function Cursor() {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!matchMedia('(hover:hover)').matches) return
    const el = ref.current!
    let cx = innerWidth / 2, cy = innerHeight / 2, tx = cx, ty = cy, raf = 0
    const move = (e: MouseEvent) => { tx = e.clientX; ty = e.clientY }
    const HOT = 'a,button,input,[data-open],[data-add],.card__media'
    const over = (e: Event) => { if ((e.target as Element)?.closest?.(HOT)) el.classList.add('is-hot') }
    const out = (e: Event) => { if ((e.target as Element)?.closest?.(HOT)) el.classList.remove('is-hot') }
    const loop = () => { cx += (tx - cx) * 0.2; cy += (ty - cy) * 0.2; el.style.transform = `translate(${cx}px,${cy}px)`; raf = requestAnimationFrame(loop) }
    addEventListener('mousemove', move, { passive: true })
    document.addEventListener('mouseover', over)
    document.addEventListener('mouseout', out)
    loop()
    return () => { cancelAnimationFrame(raf); removeEventListener('mousemove', move); document.removeEventListener('mouseover', over); document.removeEventListener('mouseout', out) }
  }, [])
  return (
    <div className="cursor" ref={ref} aria-hidden>
      <span className="cursor__dot" /><span className="cursor__ring" />
    </div>
  )
}

export function Grain() { return <div className="grain" aria-hidden /> }

/** 入场 loader：拼字母 + 进度条，1.1s 后淡出。 */
export function Loader() {
  const [done, setDone] = useState(false)
  useEffect(() => { const t = setTimeout(() => setDone(true), 1100); return () => clearTimeout(t) }, [])
  return (
    <div className={`loader${done ? ' is-done' : ''}`} aria-hidden>
      <div className="loader__mark">{'GOMALL'.split('').map((c, i) => <span key={i}>{c}</span>)}</div>
      <div className="loader__bar"><i /></div>
    </div>
  )
}

const TICKS = [
  '顺丰至臻配送 · COMPLIMENTARY DELIVERY',
  '新章 · THE APOGEE COLLECTION',
  '链上溯源 · AUTHENTICITY ON-CHAIN',
  '限量造物 · LIMITED EDITIONS',
]
export function Ticker() {
  const seq = [...TICKS, ...TICKS]
  return (
    <div className="ticker" aria-hidden>
      <div className="ticker__track">
        {seq.map((t, i) => (
          <span key={i} style={{ display: 'contents' }}>
            <span>{t}</span><span className="ticker__star">✦</span>
          </span>
        ))}
      </div>
    </div>
  )
}
