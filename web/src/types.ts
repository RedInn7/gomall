export type Cat = 'objet' | 'atelier' | 'edition' | 'voyage'

export interface Product {
  id: number
  name: string
  cat: Cat
  price: number
  off: number          // 折后价；0 表示无折扣
  img: string          // 文件名，位于 /assets/products/
  desc: string
  sold?: boolean
  span?: 'wide' | 'tall' | ''
}

export interface CartLine {
  key: string          // id|size
  id: number
  qty: number
  size: string
}

export const CAT_LABEL: Record<Cat, string> = {
  objet: '器物 OBJET',
  atelier: '手作 ATELIER',
  edition: '特别版 ÉDITION',
  voyage: '旅程 VOYAGE',
}
