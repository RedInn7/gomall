import { PRODUCTS } from '../data/products'
import type { Cat, Product } from '../types'
import { api } from './client'

const cats: Cat[] = ['objet', 'atelier', 'edition', 'voyage']

export function mapProduct(raw: any, index = 0): Product {
  const item = raw?.product ?? raw
  return {
    id: Number(item?.id || index + 1),
    name: item?.title || item?.name || `Objet ${index + 1}`,
    cat: cats[index % cats.length],
    price: Math.round(Number(item?.price) || 0),
    off: item?.discount_price ? Math.round(Number(item.discount_price)) : 0,
    img: item?.img_path || PRODUCTS[index % PRODUCTS.length].img,
    span: index % 4 === 0 ? 'wide' : '',
    desc: item?.info || item?.title || '来自工坊的造物。',
    sold: item?.on_sale === false,
  }
}

const itemsOf = (data: any) => {
  const raw = data?.item ?? data?.list ?? data ?? []
  return Array.isArray(raw) ? raw : []
}

export async function listProducts() {
  const data = await api('GET', '/api/v1/product/list', { page_size: 12, page_num: 1 })
  return itemsOf(data).map(mapProduct)
}

export async function getProduct(id: number) {
  const data = await api('GET', '/api/v1/product/show', { id })
  return mapProduct(data)
}

export async function searchProducts(query: string) {
	const data = await api('POST', '/api/v1/product/semantic-search', { query, top_k: 12 })
	return itemsOf(data).map(mapProduct)
}
