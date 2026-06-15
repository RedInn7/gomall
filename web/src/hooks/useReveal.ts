import { useEffect, useRef } from 'react'

const SAFETY_MS = 2200 // 兜底：无论如何，到点把仍隐藏的元素揭示，内容绝不卡在 opacity:0

function reveal(el: Element) { el.classList.add('in') }
function inViewport(el: Element, slack = 0.95) {
  const r = el.getBoundingClientRect()
  return r.top < innerHeight * slack && r.bottom > 0
}

function wire(targets: Element[]) {
  // 1) 挂载即在视口内的（首屏/近屏）立刻揭示——不依赖 IO 回调
  targets.forEach((t) => { if (inViewport(t)) reveal(t) })
  // 2) 其余交给 IntersectionObserver 做滚动揭示
  const io = new IntersectionObserver(
    (ents) => ents.forEach((e) => { if (e.isIntersecting) { reveal(e.target); io.unobserve(e.target) } }),
    { threshold: 0.1, rootMargin: '0px 0px -6% 0px' },
  )
  targets.forEach((t) => { if (!t.classList.contains('in')) io.observe(t) })
  // 3) 兜底：到点全部揭示（防 IO 在某些环境不触发）
  const safety = window.setTimeout(() => targets.forEach(reveal), SAFETY_MS)
  return () => { io.disconnect(); clearTimeout(safety) }
}

/** 单个元素的滚动揭示。 */
export function useReveal<T extends HTMLElement>(deps: unknown[] = []) {
  const ref = useRef<T | null>(null)
  useEffect(() => {
    if (!ref.current) return
    return wire([ref.current])
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
  return ref
}

/** 容器内所有 .reveal / .card 子元素的滚动揭示（用于动态列表）。 */
export function useRevealAll<T extends HTMLElement>(deps: unknown[] = []) {
  const ref = useRef<T | null>(null)
  useEffect(() => {
    if (!ref.current) return
    const targets = [...ref.current.querySelectorAll('.reveal, .card')]
    return wire(targets)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
  return ref
}
