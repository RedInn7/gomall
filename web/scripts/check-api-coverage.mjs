import { readFileSync, readdirSync } from 'node:fs'
import { join, resolve } from 'node:path'

const root = resolve(process.cwd(), '..')
const routeFiles = [
  ...readdirSync(join(root, 'internal'), { withFileTypes: true })
    .filter((entry) => entry.isDirectory())
    .map((entry) => join(root, 'internal', entry.name, 'routes.go')),
  join(root, 'service/search/routes.go'),
]

const backend = new Set()
for (const file of routeFiles) {
  let source
  try { source = readFileSync(file, 'utf8') } catch { continue }
  const pattern = /(public|authed|merchant|admin)\.(GET|POST|PUT|PATCH|DELETE)\("?([^"\n]+)"?/g
  for (const match of source.matchAll(pattern)) {
    const [, group, method, rawPath] = match
    if (rawPath.startsWith('webhooks/')) continue
    const clean = rawPath.replace(/^\//, '')
    backend.add(`${method} /api/v1/${group === 'admin' ? `admin/${clean}` : clean}`)
  }
}

let frontendSource = ''
try { frontendSource = readFileSync(join(process.cwd(), 'src/api/endpoints.ts'), 'utf8') } catch {}
const frontend = new Set(
  [...frontendSource.matchAll(/\['([A-Z]+)',\s*'([^']+)'/g)]
    .map((match) => `${match[1]} ${match[2]}`),
)

const missing = [...backend].filter((route) => !frontend.has(route)).sort()
if (missing.length) {
  console.error(`前端尚未接入 ${missing.length} 个后端接口：`)
  for (const route of missing) console.error(`- ${route}`)
  process.exit(1)
}

console.log(`API coverage OK: ${backend.size} backend routes are represented in the frontend.`)
