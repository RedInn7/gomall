import type { Product } from '../types'

export const yuan = (n: number) => '¥' + Number(n).toLocaleString('zh-CN')

// 资源路径带上 Vite base（/app/），public/ 下的文件部署后在 /app/assets/...
export const asset = (file: string) => `${import.meta.env.BASE_URL}assets/products/${file}`

export const priceOf = (p: Product) => p.off || p.price

export const cx = (...xs: (string | false | undefined | null)[]) => xs.filter(Boolean).join(' ')

export const EDITIONS = ['Standard', 'Numbered', 'Gift-boxed'] as const

export const no3 = (id: number) => 'N°' + String(id).padStart(3, '0')
