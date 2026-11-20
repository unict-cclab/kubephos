// Tolerant, static reader of Locust files: discovers the task tree (User classes,
// TaskSets, `tasks = {...}` / `tasks = [...]`, `@task` / `@task(n)` methods) and the
// HTTP calls each task makes (`.client.get/post/...`), following helper functions
// and methods a few levels deep. It never executes Python: loops, conditionals and
// computed weights are read as written, so the result is a default mix, not a trace.

export interface LocustCall {
  method: string
  // Literal path, or the literal prefix of a computed one (see `dynamic`).
  path: string
  dynamic?: boolean
  // Locust `name=` argument: the label under which Locust reports the request.
  name?: string
}

export interface LocustTaskNode {
  name: string
  weight: number
  kind: 'task' | 'taskset'
  children?: LocustTaskNode[]
}

export interface LocustUserClass {
  name: string
  weight: number
  tasks: LocustTaskNode[]
}

export interface LocustTask {
  name: string
  // Weight written in the Locust file (inside its TaskSet or User class).
  weight: number
  // Probability that an execution picked by Locust is this task (all classes, sum 1).
  share: number
  // Enclosing User / TaskSet classes, outermost first.
  parents: string[]
  calls: LocustCall[]
}

export interface LocustParseResult {
  status: 'ok' | 'partial' | 'failed'
  tasks: LocustTask[]
  users: LocustUserClass[]
  warnings: string[]
}

interface Line {indent: number; text: string; children: Line[]}

interface FunctionInfo {name: string; params: Array<{name: string; default?: string}>; body: string[]; decorators: string[]}

interface ClassInfo {
  name: string
  bases: string[]
  decorators: string[]
  methods: Map<string, FunctionInfo>
  attributes: Map<string, string>
  order: Array<{kind: 'method' | 'class'; name: string}>
  nested: ClassInfo[]
}

type Binding = {path: string; dynamic: boolean}

const userRoots = new Set(['User', 'HttpUser', 'FastHttpUser'])
const taskSetRoots = new Set(['TaskSet', 'SequentialTaskSet', 'MarkovTaskSet'])
const maxHelperDepth = 3

// Splits the source into logical lines (joined across brackets, backslashes and
// multi-line strings) without comments, then nests them by indentation.
export function logicalLines(source: string): Array<{indent: number; text: string}> {
  const result: Array<{indent: number; text: string}> = []
  let text = ''
  let indent = -1
  let depth = 0
  let column = 0
  let atLineStart = true
  let quote = ''
  let index = 0
  const flush = () => {
    if (text.trim()) result.push({indent: Math.max(0, indent), text: text.trim()})
    text = ''
    indent = -1
  }
  while (index < source.length) {
    const char = source[index]
    if (quote) {
      if (char === '\\') { text += source.slice(index, index + 2); index += 2; continue }
      if (source.startsWith(quote, index)) { text += quote; index += quote.length; quote = ''; continue }
      text += char === '\n' ? ' ' : char
      index++
      continue
    }
    if (atLineStart) {
      if (char === ' ') { column++; index++; continue }
      if (char === '\t') { column = (Math.floor(column / 8) + 1) * 8; index++; continue }
      if (char === '\r') { index++; continue }
      atLineStart = false
      if (indent < 0 && char !== '\n' && char !== '#') indent = column
    }
    if (char === '#') {
      while (index < source.length && source[index] !== '\n') index++
      continue
    }
    if (char === '\\' && (source[index + 1] === '\n' || source[index + 1] === '\r' && source[index + 2] === '\n')) {
      index += source[index + 1] === '\r' ? 3 : 2
      text += ' '
      continue
    }
    if (char === '\n') {
      index++
      column = 0
      if (depth > 0) { text += ' '; continue }
      atLineStart = true
      flush()
      continue
    }
    if (char === '"' || char === "'") {
      quote = source.startsWith(char.repeat(3), index) ? char.repeat(3) : char
      text += quote
      index += quote.length
      continue
    }
    if ('([{'.includes(char)) depth++
    if (')]}'.includes(char)) depth = Math.max(0, depth - 1)
    text += char
    index++
  }
  flush()
  return result
}

function nest(lines: Array<{indent: number; text: string}>): Line[] {
  const root: Line = {indent: -1, text: '', children: []}
  const stack: Line[] = [root]
  for (const value of lines) {
    const line: Line = {...value, children: []}
    while (stack.length > 1 && stack.at(-1)!.indent >= line.indent) stack.pop()
    stack.at(-1)!.children.push(line)
    stack.push(line)
  }
  return root.children
}

function flatten(lines: Line[]): string[] {
  return lines.flatMap(line => [line.text, ...flatten(line.children)])
}

// Index of the bracket closing the one at `open`, ignoring brackets in strings.
function closing(text: string, open: number): number {
  let depth = 0
  let quote = ''
  for (let index = open; index < text.length; index++) {
    const char = text[index]
    if (quote) {
      if (char === '\\') index++
      else if (text.startsWith(quote, index)) { index += quote.length - 1; quote = '' }
      continue
    }
    if (char === '"' || char === "'") { quote = text.startsWith(char.repeat(3), index) ? char.repeat(3) : char; index += quote.length - 1; continue }
    if ('([{'.includes(char)) depth++
    if (')]}'.includes(char) && --depth === 0) return index
  }
  return -1
}

// Splits on a separator outside brackets and strings.
export function splitTopLevel(text: string, separator: string): string[] {
  const parts: string[] = []
  let depth = 0
  let quote = ''
  let start = 0
  for (let index = 0; index < text.length; index++) {
    const char = text[index]
    if (quote) {
      if (char === '\\') index++
      else if (text.startsWith(quote, index)) { index += quote.length - 1; quote = '' }
      continue
    }
    if (char === '"' || char === "'") { quote = text.startsWith(char.repeat(3), index) ? char.repeat(3) : char; index += quote.length - 1; continue }
    if ('([{'.includes(char)) depth++
    else if (')]}'.includes(char)) depth--
    else if (depth === 0 && text.startsWith(separator, index)) {
      // `==`, `<=`, `>=`, `!=` are comparisons, not assignments.
      if (separator === '=' && (text[index + 1] === '=' || '=<>!'.includes(text[index - 1] ?? ''))) continue
      parts.push(text.slice(start, index))
      start = index + separator.length
      index += separator.length - 1
    }
  }
  parts.push(text.slice(start))
  return parts.map(part => part.trim()).filter((part, index, items) => part || index < items.length - 1)
}

function header(text: string, keyword: 'def' | 'class'): {name: string; inside: string; rest: string} | undefined {
  const match = (keyword === 'def' ? /^(?:async\s+)?def\s+([A-Za-z_]\w*)\s*/ : /^class\s+([A-Za-z_]\w*)\s*/).exec(text)
  if (!match) return undefined
  let position = match[0].length
  let inside = ''
  if (text[position] === '(') {
    const end = closing(text, position)
    if (end < 0) return undefined
    inside = text.slice(position + 1, end)
    position = end + 1
  }
  const colon = text.indexOf(':', position)
  if (colon < 0) return undefined
  return {name: match[1], inside, rest: text.slice(colon + 1).trim()}
}

function readFunction(line: Line, decorators: string[]): FunctionInfo | undefined {
  const parsed = header(line.text, 'def')
  if (!parsed) return undefined
  const params = splitTopLevel(parsed.inside, ',').filter(Boolean).flatMap(param => {
    const [declaration, ...defaults] = splitTopLevel(param, '=')
    const name = declaration.split(':')[0].replace(/^\*+/, '').trim()
    if (!name || name === '/' || param.startsWith('*')) return []
    return [{name, ...(defaults.length ? {default: defaults.join('=')} : {})}]
  })
  return {name: parsed.name, params, body: [...(parsed.rest ? [parsed.rest] : []), ...flatten(line.children)], decorators}
}

function readClass(line: Line, decorators: string[]): ClassInfo | undefined {
  const parsed = header(line.text, 'class')
  if (!parsed) return undefined
  const info: ClassInfo = {name: parsed.name, bases: splitTopLevel(parsed.inside, ',').filter(base => base && !base.includes('=')).map(base => base.split('.').at(-1)!.trim()), decorators, methods: new Map(), attributes: new Map(), order: [], nested: []}
  let pending: string[] = []
  for (const child of line.children) {
    if (child.text.startsWith('@')) { pending.push(child.text); continue }
    const method = readFunction(child, pending)
    if (method) { info.methods.set(method.name, method); info.order.push({kind: 'method', name: method.name}); pending = []; continue }
    const nested = readClass(child, pending)
    if (nested) { info.nested.push(nested); info.order.push({kind: 'class', name: nested.name}); pending = []; continue }
    pending = []
    const assignment = /^([A-Za-z_]\w*)\s*(?::[^=]+)?=(?!=)\s*(.+)$/.exec(child.text)
    if (assignment) info.attributes.set(assignment[1], [assignment[2], ...flatten(child.children)].join(' '))
  }
  return info
}

function stringLiteral(text: string): {value: string; formatted: boolean} | undefined {
  const match = /^([rRbBuUfF]{0,2})("""|'''|"|')/.exec(text)
  if (!match) return undefined
  const quote = match[2]
  const start = match[0].length
  let index = start
  while (index < text.length && !text.startsWith(quote, index)) index += text[index] === '\\' ? 2 : 1
  if (index >= text.length || index + quote.length !== text.length) return undefined
  return {value: text.slice(start, index), formatted: /f/i.test(match[1])}
}

// Literal path (or prefix) of a URL expression; bindings resolve parameters that
// were passed a literal by the caller.
export function evaluatePath(expression: string, bindings: Record<string, Binding> = {}): Binding | undefined {
  const terms = splitTopLevel(expression.trim(), '+')
  let path = ''
  let dynamic = terms.length > 1
  for (const [position, term] of terms.entries()) {
    let value: Binding | undefined
    const formatCall = /^(.*?)\.format\s*\(.*\)$/.exec(term)
    const percent = splitTopLevel(term, '%')
    const literal = stringLiteral(formatCall ? formatCall[1].trim() : percent.length > 1 ? percent[0] : term)
    if (literal) {
      let text = literal.value
      const braces = literal.formatted || formatCall ? text.search(/\{(?!\{)/) : -1
      const placeholder = percent.length > 1 ? text.search(/%[-#0 +]*\d*(?:\.\d+)?[sdirfx]/) : -1
      const cut = [braces, placeholder].filter(item => item >= 0)
      if (cut.length) { text = text.slice(0, Math.min(...cut)); value = {path: text, dynamic: true} } else value = {path: text, dynamic: false}
      if (literal.formatted || formatCall) value.path = value.path.replace(/\{\{/g, '{').replace(/\}\}/g, '}')
    } else if (/^[A-Za-z_]\w*$/.test(term) && bindings[term]) value = bindings[term]
    if (!value) {
      if (position === 0) return undefined
      dynamic = true
      break
    }
    path += value.path
    if (value.dynamic) { dynamic = true; break }
  }
  return {path, dynamic}
}

function argumentsOf(text: string, open: number): {positional: string[]; keywords: Record<string, string>} | undefined {
  const end = closing(text, open)
  if (end < 0) return undefined
  const positional: string[] = []
  const keywords: Record<string, string> = {}
  for (const part of splitTopLevel(text.slice(open + 1, end), ',')) {
    if (!part) continue
    const keyword = /^([A-Za-z_]\w*)\s*=(?!=)\s*([\s\S]*)$/.exec(part)
    if (keyword) keywords[keyword[1]] = keyword[2]
    else if (!part.startsWith('*')) positional.push(part)
  }
  return {positional, keywords}
}

interface Scope {cls?: ClassInfo; bindings: Record<string, Binding>}

class LocustModule {
  functions = new Map<string, FunctionInfo>()
  classes = new Map<string, ClassInfo>()
  warnings = new Set<string>()

  constructor(source: string) {
    let pending: string[] = []
    for (const line of nest(logicalLines(source))) {
      if (line.text.startsWith('@')) { pending.push(line.text); continue }
      const fn = readFunction(line, pending)
      if (fn) this.functions.set(fn.name, fn)
      const cls = fn ? undefined : readClass(line, pending)
      if (cls) this.register(cls)
      pending = []
    }
  }

  register(cls: ClassInfo) {
    this.classes.set(cls.name, cls)
    cls.nested.forEach(nested => this.register(nested))
  }

  kind(cls: ClassInfo, seen = new Set<string>()): 'user' | 'taskset' | undefined {
    if (seen.has(cls.name)) return undefined
    seen.add(cls.name)
    for (const base of cls.bases) {
      if (userRoots.has(base)) return 'user'
      if (taskSetRoots.has(base)) return 'taskset'
      const local = this.classes.get(base)
      const kind = local && local !== cls ? this.kind(local, seen) : undefined
      if (kind) return kind
    }
    return undefined
  }

  method(cls: ClassInfo | undefined, name: string, seen = new Set<string>()): {fn: FunctionInfo; cls: ClassInfo} | undefined {
    if (!cls || seen.has(cls.name)) return undefined
    seen.add(cls.name)
    const own = cls.methods.get(name)
    if (own) return {fn: own, cls}
    for (const base of cls.bases) {
      const found = this.method(this.classes.get(base), name, seen)
      if (found) return found
    }
    return undefined
  }

  // The task entries of a class: `tasks` attribute plus decorated methods/classes,
  // inherited from local base classes when the class defines none.
  entries(cls: ClassInfo, seen = new Set<string>()): Array<{name: string; weight: number; target: {kind: 'taskset'; cls: ClassInfo} | {kind: 'task'; fn?: FunctionInfo; cls?: ClassInfo; source?: string}}> {
    if (seen.has(cls.name)) return []
    seen.add(cls.name)
    const result: ReturnType<LocustModule['entries']> = []
    const add = (reference: string, weight: number) => {
      const name = reference.trim()
      if (/^lambda\b/.test(name)) {
        const body = name.replace(/^lambda[^:]*:/, '')
        result.push({name: '<lambda>', weight, target: {kind: 'task', fn: {name: '<lambda>', params: [], body: [body], decorators: []}, cls}})
        this.warnings.add('A task is a lambda: Locust names it "<lambda>", so its weight cannot be changed individually.')
        return
      }
      const short = name.split('.').at(-1)!
      const local = this.classes.get(short)
      if (local && this.kind(local) === 'taskset') { result.push({name: short, weight, target: {kind: 'taskset', cls: local}}); return }
      const method = this.method(cls, short)
      if (method) { result.push({name: short, weight, target: {kind: 'task', fn: method.fn, cls: method.cls}}); return }
      const fn = this.functions.get(short)
      if (fn) { result.push({name: short, weight, target: {kind: 'task', fn}}); return }
      this.warnings.add(`Task "${short}" of ${cls.name} is not defined in the Locust file: its HTTP calls are unknown.`)
      result.push({name: short, weight, target: {kind: 'task', source: name}})
    }
    const tasks = cls.attributes.get('tasks')?.trim()
    if (tasks?.startsWith('{')) {
      for (const entry of splitTopLevel(tasks.slice(1, closing(tasks, 0)), ',')) {
        if (!entry) continue
        const [key, value] = splitTopLevel(entry, ':')
        add(key, this.weight(value, `${cls.name}.tasks`))
      }
    } else if (tasks?.startsWith('[') || tasks?.startsWith('(')) {
      for (const entry of splitTopLevel(tasks.slice(1, closing(tasks, 0)), ',')) {
        if (!entry) continue
        const tuple = /^\((.*)\)$/.exec(entry)
        if (tuple) {
          const [reference, weight] = splitTopLevel(tuple[1], ',')
          add(reference, this.weight(weight, `${cls.name}.tasks`))
        } else add(entry, 1)
      }
    } else if (tasks) this.warnings.add(`${cls.name}.tasks is computed (${tasks.slice(0, 40)}): its tasks were not read.`)
    for (const item of cls.order) {
      const decorators = item.kind === 'method' ? cls.methods.get(item.name)!.decorators : cls.nested.find(nested => nested.name === item.name)!.decorators
      const decorator = decorators.map(text => /^@(?:[A-Za-z_]\w*\.)*task\b\s*(?:\((.*)\))?\s*$/.exec(text)).find(Boolean)
      if (!decorator) continue
      add(item.name, decorator[1] === undefined || !decorator[1].trim() ? 1 : this.weight(decorator[1].replace(/^weight\s*=\s*/, ''), `@task on ${cls.name}.${item.name}`))
    }
    if (!result.length && !tasks) for (const base of cls.bases) {
      const local = this.classes.get(base)
      if (local) {
        const inherited = this.entries(local, seen)
        if (inherited.length) return inherited
      }
    }
    return result
  }

  weight(text: string | undefined, where: string): number {
    const value = Number((text ?? '').trim())
    if (Number.isFinite(value) && value >= 0 && (text ?? '').trim() !== '') return value
    this.warnings.add(`The weight of ${where} is not a number (${(text ?? '').trim() || 'missing'}): 1 is assumed.`)
    return 1
  }

  // HTTP calls of a callable in source order, following helpers a few levels deep.
  calls(fn: FunctionInfo, scope: Scope, depth = 0, stack: string[] = []): LocustCall[] {
    const key = `${scope.cls?.name ?? ''}.${fn.name}`
    if (stack.includes(key)) return []
    if (depth > maxHelperDepth) {
      this.warnings.add(`Helper calls nested deeper than ${maxHelperDepth} levels (from ${fn.name}) were not followed.`)
      return []
    }
    const result: LocustCall[] = []
    for (const text of fn.body) {
      const found: Array<{index: number; read: () => LocustCall[]}> = []
      for (const match of text.matchAll(/\.client\.(get|post|put|patch|delete|head|options|request)\s*\(/g)) {
        const open = match.index! + match[0].length - 1
        found.push({index: match.index!, read: () => {
          const args = argumentsOf(text, open)
          if (!args) return []
          let method = match[1].toUpperCase()
          let positional = args.positional
          if (match[1] === 'request') {
            const literal = stringLiteral((args.keywords.method ?? positional[0] ?? '').trim())
            method = literal?.value.toUpperCase() || 'REQUEST'
            if (!args.keywords.method) positional = positional.slice(1)
          }
          const expression = args.keywords.url ?? args.keywords.path ?? positional[0] ?? ''
          const path = evaluatePath(expression, scope.bindings)
          const label = args.keywords.name === undefined ? undefined : evaluatePath(args.keywords.name, scope.bindings)
          const name = label && !label.dynamic ? label.path : undefined
          return [{method, path: path?.path ?? '', ...(path && !path.dynamic ? {} : {dynamic: true}), ...(name ? {name} : {})}]
        }})
      }
      for (const match of text.matchAll(/(?<![\w.])(self\.)?([A-Za-z_]\w*)\s*\(/g)) {
        const name = match[2]
        if (['def', 'class', 'lambda', 'if', 'while', 'for', 'return', 'with', 'print'].includes(name)) continue
        const method = match[1] ? this.method(scope.cls, name) : undefined
        const helper = method?.fn ?? (match[1] ? undefined : this.functions.get(name))
        if (!helper || helper.name === fn.name && !method) continue
        const open = match.index! + match[0].length - 1
        found.push({index: match.index!, read: () => {
          const args = argumentsOf(text, open) ?? {positional: [], keywords: {}}
          const params = method ? helper.params.slice(1) : helper.params
          const bindings: Record<string, Binding> = {}
          params.forEach((param, index) => {
            const expression = args.keywords[param.name] ?? args.positional[index] ?? param.default
            const value = expression === undefined ? undefined : evaluatePath(expression, scope.bindings)
            if (value) bindings[param.name] = value
          })
          return this.calls(helper, {cls: method?.cls ?? scope.cls, bindings}, depth + 1, [...stack, key])
        }})
      }
      found.sort((left, right) => left.index - right.index).forEach(item => result.push(...item.read()))
    }
    return result
  }
}

// Probability of every leaf task given the class weights, with optional
// overrides by task / TaskSet name (like the load generator's taskWeights).
export function taskShares(users: LocustUserClass[], overrides: Record<string, number> = {}): Map<string, number> {
  const shares = new Map<string, number>()
  const weightOf = (node: LocustTaskNode) => Math.max(0, overrides[node.name] ?? node.weight)
  const walk = (nodes: LocustTaskNode[], probability: number, depth: number) => {
    const total = nodes.reduce((sum, node) => sum + weightOf(node), 0)
    if (!(total > 0) || depth > 20) return
    for (const node of nodes) {
      const share = probability * weightOf(node) / total
      if (node.kind === 'taskset') walk(node.children ?? [], share, depth + 1)
      else shares.set(node.name, (shares.get(node.name) ?? 0) + share)
    }
  }
  const total = users.reduce((sum, user) => sum + Math.max(0, user.weight), 0)
  if (total > 0) users.forEach(user => walk(user.tasks, user.weight / total, 0))
  return shares
}

// The editor shows one percentage per leaf task (its share of all executions),
// while Locust (and the load generator's taskWeights) weighs siblings inside each
// class. Converts the percentages to per-class weights: a leaf keeps its value and
// a TaskSet that has siblings weighs the sum of its leaves, so nested TaskSets
// keep the shares shown. TaskSets without siblings are left out (any weight works).
export function mixOverrides(users: LocustUserClass[], percents: Record<string, number>): Record<string, number> {
  const result: Record<string, number> = {}
  const subtotal = (nodes: LocustTaskNode[], depth: number): number => {
    let sum = 0
    for (const node of nodes) {
      if (node.kind === 'taskset') {
        const value = depth > 20 ? 0 : subtotal(node.children ?? [], depth + 1)
        if (nodes.length > 1) result[node.name] = (result[node.name] ?? 0) + value
        sum += value
      } else {
        const value = Math.max(0, percents[node.name] ?? 0)
        result[node.name] = value
        sum += value
      }
    }
    return sum
  }
  users.forEach(user => subtotal(user.tasks, 0))
  return result
}

// True when every User class that Locust can spawn keeps a task with a positive
// weight: the load generator refuses a mix that leaves a class without tasks.
export function mixRunnable(users: LocustUserClass[], overrides: Record<string, number>): boolean {
  const shares = users.filter(user => user.weight > 0).map(user => taskShares([user], overrides))
  return shares.length > 0 && shares.every(map => [...map.values()].some(value => value > 0))
}

export function parseLocustfile(source: string): LocustParseResult {
  let module: LocustModule
  try {
    module = new LocustModule(source)
  } catch (cause) {
    return {status: 'failed', tasks: [], users: [], warnings: [`The Locust file could not be read: ${cause instanceof Error ? cause.message : String(cause)}`]}
  }
  const leaves = new Map<string, LocustTask>()
  const build = (cls: ClassInfo, parents: string[]): LocustTaskNode[] => module.entries(cls).map(entry => {
    if (entry.target.kind === 'taskset') {
      if (parents.includes(entry.target.cls.name)) return {name: entry.name, weight: entry.weight, kind: 'taskset' as const, children: []}
      return {name: entry.name, weight: entry.weight, kind: 'taskset' as const, children: build(entry.target.cls, [...parents, entry.target.cls.name])}
    }
    if (!leaves.has(entry.name)) {
      const calls = entry.target.fn ? module.calls(entry.target.fn, {cls: entry.target.cls ?? cls, bindings: {}}) : []
      if (entry.target.fn && !calls.length) module.warnings.add(`No HTTP call was found in task "${entry.name}".`)
      leaves.set(entry.name, {name: entry.name, weight: entry.weight, share: 0, parents, calls})
    }
    return {name: entry.name, weight: entry.weight, kind: 'task' as const}
  })
  const classes = [...module.classes.values()]
  let roots = classes.filter(cls => module.kind(cls) === 'user' && !/^True$/.test(cls.attributes.get('abstract')?.trim() ?? ''))
  if (!roots.length) {
    const referenced = new Set(classes.flatMap(cls => module.entries(cls).map(entry => entry.name)))
    roots = classes.filter(cls => module.kind(cls) === 'taskset' && !referenced.has(cls.name))
    if (roots.length) module.warnings.add('No User class was found: top-level TaskSets are used as users.')
  }
  const users = roots.map(cls => {
    const declared = cls.attributes.get('weight')
    const weight = declared === undefined ? 1 : module.weight(declared, `${cls.name}.weight`)
    return {name: cls.name, weight, tasks: build(cls, [cls.name])}
  }).filter(user => user.tasks.length)
  const shares = taskShares(users)
  const tasks = [...leaves.values()].map(task => ({...task, share: shares.get(task.name) ?? 0}))
  const warnings = [...module.warnings]
  if (!tasks.length) return {status: 'failed', tasks: [], users: [], warnings: [roots.length || classes.length ? 'No Locust task was found in the User classes of this file.' : 'No Locust User class was found in this file.', ...warnings]}
  return {status: warnings.length ? 'partial' : 'ok', tasks, users, warnings}
}

// Number of HTTP requests one execution of the task sends, as written (≥ 1 assumed
// when the calls are unknown, so the task still counts as one request).
export function requestsPerExecution(task: LocustTask): number {
  return Math.max(1, task.calls.length)
}
