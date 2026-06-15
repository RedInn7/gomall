import { useCallback, useEffect, useMemo, useState } from 'react'
import type { CartLine, Product } from '../types'
import { priceOf } from '../lib/util'

const KEY = 'gomall_cart'
const load = (): CartLine[] => {
  try { return JSON.parse(localStorage.getItem(KEY) || '[]') } catch { return [] }
}

export function useCart(findP: (id: number) => Product | undefined) {
  const [lines, setLines] = useState<CartLine[]>(load)

  useEffect(() => { localStorage.setItem(KEY, JSON.stringify(lines)) }, [lines])

  const add = useCallback((id: number, qty = 1, size = 'Standard') => {
    setLines((prev) => {
      const key = `${id}|${size}`
      const i = prev.findIndex((l) => l.key === key)
      if (i >= 0) {
        const next = prev.slice()
        next[i] = { ...next[i], qty: next[i].qty + qty }
        return next
      }
      return [...prev, { key, id, qty, size }]
    })
  }, [])

  const setQty = useCallback((key: string, qty: number) => {
    setLines((prev) => prev
      .map((l) => (l.key === key ? { ...l, qty } : l))
      .filter((l) => l.qty > 0))
  }, [])

  const clear = useCallback(() => setLines([]), [])

  const count = useMemo(() => lines.reduce((s, l) => s + l.qty, 0), [lines])
  const total = useMemo(
    () => lines.reduce((s, l) => { const p = findP(l.id); return s + (p ? priceOf(p) * l.qty : 0) }, 0),
    [lines, findP],
  )

  return { lines, add, setQty, clear, count, total }
}
