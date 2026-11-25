// Locust files used by the parser tests: the upstream Online Boutique load
// generator and the scripts embedded in the catalog application descriptors.
import onlineBoutique from './fixtures/online-boutique-locustfile.py?raw'

// Read with Node at test time (Vite refuses imports outside the frontend root):
// the tests always see the descriptors exactly as shipped in the catalog.
const fs = (globalThis as unknown as {process: {getBuiltinModule: (id: string) => {readFileSync: (path: URL, encoding: 'utf8') => string}}}).process.getBuiltinModule('node:fs')
const here = import.meta.url
const catalogDescriptor = (id: string) => fs.readFileSync(new URL(['..', '..', '..', 'catalog', 'applications', id, 'application.yaml'].join('/'), here), 'utf8')
const bookinfoDescriptor = catalogDescriptor('bookinfo')
const trainTicketDescriptor = catalogDescriptor('train-ticket')

// Extracts the `content: |` block scalar following the given script path.
export function embeddedScript(descriptor: string, script: string): string {
  const lines = descriptor.split('\n')
  const start = lines.findIndex(line => line.trim() === `script: ${script}`)
  const header = lines.findIndex((line, index) => index > start && /^\s*content:\s*\|\s*$/.test(line))
  if (start < 0 || header < 0) throw new Error(`No embedded content for ${script}`)
  const keyIndent = lines[header].search(/\S/)
  const body: string[] = []
  for (const line of lines.slice(header + 1)) {
    if (line.trim() && line.search(/\S/) <= keyIndent) break
    body.push(line)
  }
  const indent = Math.min(...body.filter(line => line.trim()).map(line => line.search(/\S/)))
  return body.map(line => line.slice(indent)).join('\n')
}

export const onlineBoutiqueLocustfile = onlineBoutique
export const bookinfoLocustfile = embeddedScript(bookinfoDescriptor, 'kubephos/bookinfo-locustfile.py')
export const trainTicketLocustfile = embeddedScript(trainTicketDescriptor, 'kubephos/train-ticket-locustfile.py')
