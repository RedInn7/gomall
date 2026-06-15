import { useEffect, useState } from 'react'

interface Props {
  cartCount: number
  onSearch: () => void
  onCart: () => void
  onNav: (hash: string) => void
}

export function Nav({ cartCount, onSearch, onCart, onNav }: Props) {
  const [stuck, setStuck] = useState(false)
  useEffect(() => {
    const fn = () => setStuck(scrollY > 40)
    addEventListener('scroll', fn, { passive: true })
    return () => removeEventListener('scroll', fn)
  }, [])

  const go = (e: React.MouseEvent, hash: string) => { e.preventDefault(); onNav(hash) }

  return (
    <header className={`nav${stuck ? ' is-stuck' : ''}`}>
      <nav className="nav__col nav__col--left">
        <a href="#collection" onClick={(e) => go(e, '#collection')}>典藏 COLLECTION</a>
        <a href="#manifest" onClick={(e) => go(e, '#manifest')}>主张 MAISON</a>
        <a href="#promise" onClick={(e) => go(e, '#promise')}>承诺 PROMISE</a>
      </nav>
      <a href="#top" className="nav__brand" onClick={(e) => go(e, '#top')} aria-label="GOMALL Atelier">
        GOMALL<sup>®</sup>
      </a>
      <div className="nav__col nav__col--right">
        <button className="nav__icon" onClick={onSearch} aria-label="搜索">
          <svg viewBox="0 0 24 24"><circle cx="11" cy="11" r="7" /><line x1="16.5" y1="16.5" x2="21" y2="21" /></svg>
          <span>搜索</span>
        </button>
        <button className="nav__icon" onClick={onCart} aria-label="购物袋">
          <svg viewBox="0 0 24 24"><path d="M6 8h12l-1 13H7L6 8z" /><path d="M9 8V6a3 3 0 0 1 6 0v2" /></svg>
          <span>购物袋</span>
          <i className={`nav__count${cartCount > 0 ? ' show' : ''}`}>{cartCount}</i>
        </button>
      </div>
    </header>
  )
}
