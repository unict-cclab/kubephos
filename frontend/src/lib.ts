import type {Application, Experiment, JsonSchema, SchemaProperty} from './types'

// Dates follow the interface language, like relative times and headings, with a
// 24-hour clock that reads unambiguously in logs and tables: "Oct 28, 2026, 16:16".
const dateFormatter = new Intl.DateTimeFormat('en', {dateStyle: 'medium', timeStyle: 'short', hourCycle: 'h23'})

export function formatDate(value: string): string {
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? dateFormatter.format(date) : '—'
}

const relativeFormatter = new Intl.RelativeTimeFormat('en', {numeric: 'auto'})
const relativeUnits: Array<[Intl.RelativeTimeFormatUnit, number]> = [['year', 31536000], ['month', 2592000], ['week', 604800], ['day', 86400], ['hour', 3600], ['minute', 60]]

// "5 minutes ago", "yesterday"; anything under a minute reads "just now".
export function formatRelativeTime(value: string, now = Date.now()): string {
  const time = new Date(value).getTime()
  if (!Number.isFinite(time)) return '—'
  const seconds = Math.round((time - now) / 1000)
  const unit = relativeUnits.find(([, size]) => Math.abs(seconds) >= size)
  return unit ? relativeFormatter.format(Math.round(seconds / unit[1]), unit[0]) : 'just now'
}

export function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`
  return `${(value / 1024).toFixed(1)} KB`
}

export function isTerminalExperiment(status: string): boolean {
  return ['succeeded', 'failed', 'canceled'].includes(status.trim().toLowerCase())
}

export function experimentDeletionMessage(experiment: Pick<Experiment, 'name' | 'kind'>): string {
  return `Delete ${experiment.kind === 'suite' ? 'suite' : 'experiment'} ${experiment.name} and its runs, logs and collected artifacts?`
}

export function experimentDisplayStatus(experiment: Pick<Experiment, 'status' | 'cancelRequested'>): string {
  return experiment.cancelRequested && ['queued', 'running', 'initializing', 'initialized'].includes(experiment.status) ? 'stopping' : experiment.status
}

export function humanize(value: string): string {
  return value.replace(/([a-z])([A-Z])/g, '$1 $2').replace(/^./, letter => letter.toUpperCase())
}

export function readSchemaValues(form: HTMLFormElement, schema: JsonSchema, prefix = 'schema', applications: Application[] = []): Record<string, unknown> {
	const result: Record<string, unknown> = {}
	for (const [name, property] of Object.entries(schema.properties ?? {})) {
		const schemaSource = property['x-kubephos-schema-from-application']
		if (schemaSource) {
			const source = form.elements.namedItem(`${prefix}.${schemaSource}`)
			const reference = source instanceof HTMLInputElement || source instanceof HTMLTextAreaElement || source instanceof HTMLSelectElement ? source.value : ''
			const application = applications.find(item => item.reference === reference)
			const dynamicSchema = application?.descriptor.spec?.valuesSchema
			if (dynamicSchema) result[name] = readSchemaValues(form, dynamicSchema, `${prefix}.${name}`, applications)
			continue
		}
		if (property.type === 'object' && property.properties) {
			result[name] = readSchemaValues(form, property, `${prefix}.${name}`, applications)
			continue
		}
		const field = form.elements.namedItem(`${prefix}.${name}`)
    if (!(field instanceof HTMLInputElement || field instanceof HTMLTextAreaElement || field instanceof HTMLSelectElement)) continue
    const value = readValue(field, property)
    if (value !== undefined) result[name] = value
  }
  return result
}

function readValue(field: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement, property: SchemaProperty): unknown {
  if (property.type === 'boolean') return field instanceof HTMLInputElement && field.type === 'checkbox' ? field.checked : field.value === 'true'
  if (field.value === '') return undefined
  if (property.type === 'integer') return Number.parseInt(field.value, 10)
  if (property.type === 'number') return Number(field.value)
  if (property.type === 'object' || property.type === 'array') return JSON.parse(field.value)
  return field.value
}
