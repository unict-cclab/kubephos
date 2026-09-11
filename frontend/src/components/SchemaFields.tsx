import {useState} from 'react'
import type {Application, Artifact, Connection, Credential, JsonSchema, SchemaProperty} from '../types'
import {humanize} from '../lib'

interface SchemaFieldsProps {
  schema: JsonSchema
  prefix?: string
  values?: Record<string, unknown>
  applications: Application[]
  artifacts: Artifact[]
  connections: Connection[]
  credentials: Credential[]
}

export function SchemaFields({schema, prefix = 'schema', values = {}, applications, artifacts, connections, credentials}: SchemaFieldsProps) {
  const initialValues = schemaValues(schema, values)
  const signature = JSON.stringify({schema, initialValues})
  return <SchemaFieldSet key={signature} schema={schema} prefix={prefix} initialValues={initialValues} applications={applications} artifacts={artifacts} connections={connections} credentials={credentials} />
}

interface SchemaFieldSetProps extends Omit<SchemaFieldsProps, 'values'> {
  initialValues: Record<string, unknown>
}

function SchemaFieldSet({schema, prefix = 'schema', initialValues, applications, artifacts, connections, credentials}: SchemaFieldSetProps) {
  const required = new Set(schema.required ?? [])
  const [currentValues, setCurrentValues] = useState<Record<string, unknown>>(initialValues)
  return <div className="schema-fields">
    {Object.entries(schema.properties ?? {}).filter(([, property]) => visible(property, currentValues)).map(([name, property]) => <SchemaField
      key={name}
      name={name}
      prefix={prefix}
      property={property}
      value={currentValues[name]}
      required={required.has(name)}
      onValue={value => setCurrentValues(current => ({...current, [name]: value}))}
      applications={applications}
      artifacts={artifacts}
      connections={connections}
      credentials={credentials}
    />)}
  </div>
}

interface SchemaFieldProps extends Omit<SchemaFieldsProps, 'schema'> {
  name: string
  property: SchemaProperty
  value?: unknown
  required: boolean
  onValue: (value: unknown) => void
}

function SchemaField({name, prefix, property, value, required, onValue, applications, artifacts, connections, credentials}: SchemaFieldProps) {
  const title = property.title ?? humanize(name)
  const fieldName = `${prefix}.${name}`
  const wide = ['string', 'object', 'array'].includes(property.type ?? 'string') ? 'wide' : ''
  const hint = property.description && <small>{property.description}</small>
  let choices: Array<{value: string; label: string}> | null = null

  if (property.format === 'kubephos-connection-ref') {
    const provider = property['x-kubephos-provider']
    choices = connections.filter(item => !provider || item.provider === provider).map(item => ({value: item.id, label: `${item.name} · ${item.provider}`}))
  } else if (property.format === 'kubephos-secret-ref') {
    const kind = property['x-kubephos-secret-kind']
    choices = credentials.filter(item => item.kind === kind).map(item => ({value: item.id, label: `${item.name} · ${item.fingerprint}`}))
  } else if (property.format === 'kubephos-application-ref') {
    const trait = property['x-kubephos-required-trait']
    choices = applications
      .filter(item => !trait || item.descriptor.spec?.interface?.components?.some(component => component.traits?.includes(trait)))
      .map(item => ({value: item.reference, label: `${item.name} · ${item.version}`}))
  } else if (property.format === 'kubephos-artifact-ref') {
    const type = property['x-kubephos-artifact-type']
    const version = property['x-kubephos-artifact-version']
    choices = artifacts
      .filter(item => (!type || item.type === type) && (!version || item.version === version))
      .map(item => ({value: item.id, label: `${item.name} · ${item.type}/${item.version} · ${item.operationId.slice(0, 12)}…`}))
  } else if (property.enum) {
    choices = property.enum.map(value => ({value: String(value), label: String(value)}))
  }

  if (choices) {
    return <label className={wide}>{title}<select name={fieldName} required={required} disabled={!choices.length} defaultValue={String(value ?? property.default ?? choices[0]?.value ?? '')} onChange={event => onValue(schemaValue(event.target.value, property))}>
      {choices.length ? choices.map(choice => <option key={choice.value} value={choice.value}>{choice.label}</option>) : <option value="">No compatible value available</option>}
    </select>{hint}</label>
  }
  if (property.type === 'boolean') {
    return <label className="checkbox-field wide"><input name={fieldName} type="checkbox" defaultChecked={Boolean(value ?? property.default)} onChange={event => onValue(event.target.checked)} />{title}{hint}</label>
  }
  if (property.type === 'object' || property.type === 'array') {
    const serialized = value === undefined && property.default === undefined ? '' : JSON.stringify(value ?? property.default, null, 2)
    return <label className="wide">{title}<textarea name={fieldName} rows={4} required={required} defaultValue={serialized} onChange={event => onValue(event.target.value)} />{hint}</label>
  }
  if (property.type === 'string' && property['x-kubephos-multiline']) {
    return <label className="wide">{title}<textarea name={fieldName} rows={5} required={required} defaultValue={value === undefined && property.default === undefined ? '' : String(value ?? property.default)} minLength={property.minLength} maxLength={property.maxLength} onChange={event => onValue(event.target.value)} />{hint}</label>
  }
  const numeric = property.type === 'integer' || property.type === 'number'
  return <label className={wide}>{title}<input
    name={fieldName}
    type={numeric ? 'number' : property.writeOnly ? 'password' : 'text'}
    required={required}
    defaultValue={value === undefined && property.default === undefined ? '' : String(value ?? property.default)}
    min={property.minimum}
    max={property.maximum}
    minLength={property.minLength}
    maxLength={property.maxLength}
    onChange={event => onValue(schemaValue(event.target.value, property))}
  />{hint}</label>
}

function schemaValues(schema: JsonSchema, values: Record<string, unknown>): Record<string, unknown> {
  const result: Record<string, unknown> = {}
  for (const [name, property] of Object.entries(schema.properties ?? {})) {
    if (values[name] !== undefined) result[name] = values[name]
    else if (property.default !== undefined) result[name] = property.default
    else if (property.enum?.length) result[name] = property.enum[0]
  }
  return result
}

function visible(property: SchemaProperty, values: Record<string, unknown>): boolean {
  const condition = property['x-kubephos-visible-when']
  return !condition || condition.values.some(value => Object.is(value, values[condition.property]))
}

function schemaValue(value: string, property: SchemaProperty): unknown {
  if (property.type === 'integer') return Number.parseInt(value, 10)
  if (property.type === 'number') return Number(value)
  return value
}
