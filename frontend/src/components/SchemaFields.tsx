import type {Application, Connection, Credential, JsonSchema, SchemaProperty} from '../types'
import {humanize} from '../lib'

interface SchemaFieldsProps {
  schema: JsonSchema
  prefix?: string
  applications: Application[]
  connections: Connection[]
  credentials: Credential[]
}

export function SchemaFields({schema, prefix = 'schema', applications, connections, credentials}: SchemaFieldsProps) {
  const required = new Set(schema.required ?? [])
  return <div className="schema-fields">
    {Object.entries(schema.properties ?? {}).map(([name, property]) => <SchemaField
      key={name}
      name={name}
      prefix={prefix}
      property={property}
      required={required.has(name)}
      applications={applications}
      connections={connections}
      credentials={credentials}
    />)}
  </div>
}

interface SchemaFieldProps extends Omit<SchemaFieldsProps, 'schema'> {
  name: string
  property: SchemaProperty
  required: boolean
}

function SchemaField({name, prefix, property, required, applications, connections, credentials}: SchemaFieldProps) {
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
  } else if (property.enum) {
    choices = property.enum.map(value => ({value: String(value), label: String(value)}))
  }

  if (choices) {
    return <label className={wide}>{title}<select name={fieldName} required={required} disabled={!choices.length} defaultValue={String(property.default ?? choices[0]?.value ?? '')}>
      {choices.length ? choices.map(choice => <option key={choice.value} value={choice.value}>{choice.label}</option>) : <option value="">No compatible value available</option>}
    </select>{hint}</label>
  }
  if (property.type === 'boolean') {
    return <label className="checkbox-field wide"><input name={fieldName} type="checkbox" defaultChecked={Boolean(property.default)} />{title}{hint}</label>
  }
  if (property.type === 'object' || property.type === 'array') {
    const value = property.default === undefined ? '' : JSON.stringify(property.default, null, 2)
    return <label className="wide">{title}<textarea name={fieldName} rows={4} required={required} defaultValue={value} />{hint}</label>
  }
  const numeric = property.type === 'integer' || property.type === 'number'
  return <label className={wide}>{title}<input
    name={fieldName}
    type={numeric ? 'number' : property.writeOnly ? 'password' : 'text'}
    required={required}
    defaultValue={property.default === undefined ? '' : String(property.default)}
    min={property.minimum}
    max={property.maximum}
    minLength={property.minLength}
    maxLength={property.maxLength}
  />{hint}</label>
}
