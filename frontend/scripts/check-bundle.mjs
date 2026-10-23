import {readFileSync} from 'node:fs'
import {fileURLToPath} from 'node:url'
import {resolve} from 'node:path'
import {gzipSync} from 'node:zlib'

const dist = fileURLToPath(new URL('../dist/', import.meta.url))
const html = readFileSync(resolve(dist, 'index.html'), 'utf8')
const assets = [...new Set([...html.matchAll(/(?:src|href)="([^"?#]+\.(?:js|css))"/g)].map(match => match[1]))]
if (!assets.some(asset => asset.endsWith('.js'))) throw new Error('No application entry found in dist/index.html.')
const sizes = assets.map(asset => ({asset, bytes: gzipSync(readFileSync(resolve(dist, asset.replace(/^\//, '')))).length}))
const total = sizes.reduce((sum, {bytes}) => sum + bytes, 0)
for (const {asset, bytes} of sizes) console.log(`${asset}: ${(bytes / 1024).toFixed(1)} KiB gzip`)
console.log(`Initial JS + CSS: ${(total / 1024).toFixed(1)} KiB gzip (budget: 220 KiB)`)
if (total > 220 * 1024) throw new Error('Initial application assets exceed the frontend performance budget.')
