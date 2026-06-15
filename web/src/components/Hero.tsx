import { useEffect, useRef } from 'react'
import { asset } from '../lib/util'

export function Hero({ count, onNav }: { count: number; onNav: (h: string) => void }) {
  const root = useRef<HTMLElement>(null)
  const art = useRef<HTMLElement>(null)

  // 入场：按 data-d 逐项加 in
  useEffect(() => {
    const els = root.current?.querySelectorAll<HTMLElement>('[data-d]') ?? []
    const timers: number[] = []
    els.forEach((el) => {
      const d = Number(el.dataset.d || 1) * 95
      timers.push(window.setTimeout(() => el.classList.add('in'), d))
    })
    return () => timers.forEach(clearTimeout)
  }, [])

  // 母题视差
  useEffect(() => {
    if (!matchMedia('(hover:hover)').matches) return
    const fn = (e: MouseEvent) => {
      const x = e.clientX / innerWidth - 0.5, y = e.clientY / innerHeight - 0.5
      if (art.current) art.current.style.transform = `translate(${x * 18}px,${y * 18}px) rotate(${x * 2}deg)`
    }
    addEventListener('mousemove', fn, { passive: true })
    return () => removeEventListener('mousemove', fn)
  }, [])

  const go = (e: React.MouseEvent, h: string) => { e.preventDefault(); onNav(h) }

  return (
    <section className="hero" id="top" ref={root}>
      <div className="hero__frame" aria-hidden />
      <span className="hero__side hero__side--l">EST. MMXXV — 北京 · BEIJING</span>
      <span className="hero__side hero__side--r">N<sup>o</sup> 001 / APOGÉE</span>

      <div className="hero__copy">
        <p className="kicker reveal" data-d="1">星际造物所 — THE ATELIER OF ORBITS</p>
        <h1 className="hero__title">
          <span className="line"><span className="reveal-y" data-d="2">为</span> <span className="reveal-y ital" data-d="3">星海</span></span>
          <span className="line"><span className="reveal-y" data-d="4">而造的</span></span>
          <span className="line"><em className="reveal-y" data-d="5">日常物</em><span className="reveal-y dot" data-d="6">.</span></span>
        </h1>
        <p className="hero__lede reveal" data-d="7">
          GOMALL ATELIER 是一间面向星海的概念商店。每一件造物皆为限量发行，
          以编辑式的克制陈列，呈献给愿意慢下来的人。
        </p>
        <div className="hero__cta reveal" data-d="8">
          <a href="#collection" className="btn btn--gold" onClick={(e) => go(e, '#collection')}>浏览典藏 <span>→</span></a>
          <a href="#manifest" className="btn btn--ghost" onClick={(e) => go(e, '#manifest')}>品牌主张</a>
        </div>
        <div className="hero__meta reveal" data-d="9">
          <div><b>{String(count).padStart(2, '0')}</b><span>限量造物</span></div>
          <div><b>24</b><span>城臻享配送</span></div>
          <div><b>∞</b><span>链上溯源</span></div>
        </div>
      </div>

      <div className="hero__stage">
        <div className="orbit orbit--1" aria-hidden />
        <div className="orbit orbit--2" aria-hidden />
        <figure className="hero__art" ref={art}>
          <img src={asset('LV.jpg')} alt="GOMALL 母题：漂浮的旅人" />
          <figcaption>“LE VOYAGEUR” — 母题 N<sup>o</sup>1</figcaption>
        </figure>
        <div className="hero__halo" aria-hidden />
      </div>

      <a href="#collection" className="hero__scroll" onClick={(e) => go(e, '#collection')} aria-label="向下">
        <span>SCROLL</span><i />
      </a>
    </section>
  )
}
