import { asset } from '../lib/util'
import { useRevealAll } from '../hooks/useReveal'

export function Editorial({ onNav }: { onNav: (h: string) => void }) {
  const ref = useRevealAll<HTMLElement>([])
  return (
    <section className="editorial" id="manifest" ref={ref}>
      <div className="editorial__art" aria-hidden><img src={asset('prada.jpg')} alt="" /></div>
      <div className="editorial__copy">
        <p className="kicker reveal">主张 — THE MAISON</p>
        <blockquote className="editorial__quote reveal">“我们不追逐潮汐，<br />我们<em>测绘星轨</em>。”</blockquote>
        <p className="editorial__body reveal">
          在 GOMALL，速度从来不是终点。我们相信被等待过的事物自带重量——
          从一针一线到一次链上确权，皆为可被溯源的承诺。少，即是丰盈。
        </p>
        <a href="#promise" className="btn btn--ghost reveal" onClick={(e) => { e.preventDefault(); onNav('#promise') }}>关于工坊</a>
      </div>
    </section>
  )
}

const PROMISES = [
  { no: '01', h: '臻享配送', p: '24 城当日臻享，每一程皆以丝绒衬护。运费由工坊承担。' },
  { no: '02', h: '链上溯源', p: '每件造物铸有唯一凭证，确权上链，真伪自此无需言说。' },
  { no: '03', h: '终身工坊', p: '购入即入册。养护、修复与再造，工坊伴你穿越漫长岁月。' },
]
export function Promise() {
  const ref = useRevealAll<HTMLElement>([])
  return (
    <section className="promise" id="promise" ref={ref}>
      <header className="sec-head sec-head--center">
        <p className="kicker">承诺 — THE PROMISE</p>
        <h2 className="sec-title">三则<em>恒定</em></h2>
      </header>
      <div className="promise__row">
        {PROMISES.map((c) => (
          <article className="promise__card reveal" key={c.no}>
            <span className="promise__no">{c.no}</span>
            <h3>{c.h}</h3>
            <p>{c.p}</p>
          </article>
        ))}
      </div>
    </section>
  )
}

export function Signup({ toast }: { toast: (m: string) => void }) {
  const ref = useRevealAll<HTMLElement>([])
  return (
    <section className="signup" ref={ref}>
      <p className="kicker reveal">入册 — THE LEDGER</p>
      <h2 className="signup__title reveal">先于众人<em>，遇见新章。</em></h2>
      <form className="signup__form reveal" onSubmit={(e) => { e.preventDefault(); (e.target as HTMLFormElement).reset(); toast('已入册 · 新章将先达于你') }}>
        <input type="email" required placeholder="你的电子邮件 / your email" aria-label="email" />
        <button type="submit" className="btn btn--gold">入册 <span>→</span></button>
      </form>
      <p className="signup__fine reveal">凭此入册，先睹限量发售并享私享预览。随时可退。</p>
    </section>
  )
}

export function Footer({ onNav, toast }: { onNav: (h: string) => void; toast: (m: string) => void }) {
  const go = (e: React.MouseEvent, h: string) => { e.preventDefault(); onNav(h) }
  const noop = (e: React.MouseEvent) => { e.preventDefault(); toast('敬请期待 · COMING SOON') }
  return (
    <footer className="foot">
      <div className="foot__top">
        <a href="#top" className="foot__brand" onClick={(e) => go(e, '#top')}>GOMALL<sup>®</sup></a>
        <div className="foot__cols">
          <div><h4>典藏</h4>
            <a href="#collection" onClick={(e) => go(e, '#collection')}>本季列陈</a>
            <a href="#collection" onClick={(e) => go(e, '#collection')}>限量特别版</a>
            <a href="#collection" onClick={(e) => go(e, '#collection')}>旅程系列</a>
          </div>
          <div><h4>工坊</h4>
            <a href="#manifest" onClick={(e) => go(e, '#manifest')}>品牌主张</a>
            <a href="#promise" onClick={(e) => go(e, '#promise')}>三则恒定</a>
            <a href="#promise" onClick={(e) => go(e, '#promise')}>养护再造</a>
          </div>
          <div><h4>联结</h4>
            <a href="#" onClick={noop}>Instagram</a>
            <a href="#" onClick={noop}>WeChat</a>
            <a href="#" onClick={noop}>Journal</a>
          </div>
        </div>
      </div>
      <div className="foot__base">
        <span>© {new Date().getFullYear()} GOMALL ATELIER · 星际造物所</span>
        <span>CRAFTED WITH GO · GIN · 编辑式陈列</span>
      </div>
    </footer>
  )
}
