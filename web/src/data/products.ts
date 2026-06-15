import type { Product } from '../types'

// 母题（黑底白线宇航员）借 duotone 着色读作不同器物；编号即叙事。
export const PRODUCTS: Product[] = [
  { id: 1, name: 'Le Voyageur',  cat: 'voyage',  price: 4280, off: 0,    img: 'LV.jpg',     span: 'tall',
    desc: '以漂泊者为名的旗舰造物。冷光金属与哑面陶瓷的对话，献给永远在路上的人。' },
  { id: 2, name: 'Orbite N°2',   cat: 'objet',   price: 2980, off: 0,    img: 'prada.jpg',  span: '',
    desc: '环形结构桌面器物，承托微小日常。每一道弧线皆经手工抛磨。' },
  { id: 3, name: 'Apogée',       cat: 'edition', price: 6800, off: 5400, img: 'LV0.jpg',    span: '',
    desc: '远地点特别版，全球编号 100 件。配链上确权凭证与丝绒封存盒。' },
  { id: 4, name: 'Silent Drift', cat: 'atelier', price: 3560, off: 0,    img: 'prada0.jpg', span: 'wide',
    desc: '手作系列。静默漂移之姿，被定格于黄铜与冷釉之间，独一无二。' },
  { id: 5, name: 'Nébuleuse',    cat: 'objet',   price: 1980, off: 1480, img: 'LV1.jpg',    span: '',
    desc: '星云香薰石，吸附微光与气息。点燃前后皆是一件雕塑。' },
  { id: 6, name: 'Méridien',     cat: 'voyage',  price: 5200, off: 0,    img: 'prada1.jpg', span: '',
    desc: '子午线旅行匣，为长途而生。内衬绒面，外覆抗磨星砂涂层。' },
  { id: 7, name: 'Constellation', cat: 'edition', price: 7600, off: 0,   img: 'prada.jpg',  span: '', sold: true,
    desc: '星座典藏。已封存，仅余传世之姿。' },
  { id: 8, name: 'Le Calme',     cat: 'atelier', price: 2460, off: 0,    img: 'LV.jpg',     span: '',
    desc: '手作静物。极简的体量里，藏着被反复打磨的耐心。' },
]
