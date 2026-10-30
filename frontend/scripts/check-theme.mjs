import {readFileSync} from 'node:fs'

const source = readFileSync(new URL('../src/design/theme.css', import.meta.url), 'utf8')
const [lightSource, darkSource] = source.split(':root[data-theme="dark"]')
const variables = text => Object.fromEntries([...text.matchAll(/(--[\w-]+):\s*([^;]+);/g)].map(([, key, value]) => [key, value.trim()]))
const light = variables(lightSource)
const themes = {light, dark: {...light, ...variables(darkSource)}}
function luminance(value, theme) {
  if (value.startsWith('var(')) return luminance(theme[value.slice(4, -1)], theme)
  let linear
  if (/^#[\da-f]{6}$/i.test(value)) {
    linear = value.slice(1).match(/../g).map(hex => parseInt(hex, 16) / 255).map(channel => channel <= .04045 ? channel / 12.92 : ((channel + .055) / 1.055) ** 2.4)
  } else {
    const match = value.match(/^oklch\(([\d.]+) ([\d.]+) ([\d.]+)\)$/)
    if (!match) throw new Error(`Unsupported theme color: ${value}`)
    const [, L, C, H] = match.map(Number)
    const a = C * Math.cos(H * Math.PI / 180), b = C * Math.sin(H * Math.PI / 180)
    const l = (L + .3963377774 * a + .2158037573 * b) ** 3
    const m = (L - .1055613458 * a - .0638541728 * b) ** 3
    const s = (L - .0894841775 * a - 1.291485548 * b) ** 3
    linear = [4.0767416621*l - 3.3077115913*m + .2309699292*s, -1.2684380046*l + 2.6097574011*m - .3413193965*s, -.0041960863*l - .7034186147*m + 1.707614701*s].map(channel => Math.min(1, Math.max(0, channel)))
  }
  return linear.reduce((total, channel, index) => total + channel * [.2126, .7152, .0722][index], 0)
}
const pairs = [
  ['--foreground', '--background', 4.5],
  ['--muted-foreground', '--card', 4.5],
  ['--muted-foreground', '--muted', 4.5],
  ['--primary-foreground', '--primary', 4.5],
  ['--blue', '--card', 4.5],
  ['--success-foreground', '--success-background', 4.5],
  ['--error-foreground', '--error-background', 4.5],
  ['--field-border', '--card', 3],
  ['--ring', '--card', 3]
]
let failed = false
for (const [name, theme] of Object.entries(themes)) for (const [foreground, background, minimum] of pairs) {
  const a = luminance(theme[foreground], theme), b = luminance(theme[background], theme)
  const contrast = (Math.max(a, b) + .05) / (Math.min(a, b) + .05)
  console.log(`${name}: ${foreground} / ${background} = ${contrast.toFixed(2)}:1 (minimum ${minimum}:1)`)
  if (contrast < minimum) failed = true
}
if (failed) throw new Error('A theme color pair falls below its WCAG contrast threshold.')
