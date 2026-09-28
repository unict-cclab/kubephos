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
	const initialValues = schemaValues(schema, values, applications)
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
		formValues={currentValues}
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
	formValues: Record<string, unknown>
}

function SchemaField({name, prefix, property, value, required, onValue, formValues, applications, artifacts, connections, credentials}: SchemaFieldProps) {
  const title = property.title ?? humanize(name)
  const fieldName = `${prefix}.${name}`
  if (property['x-kubephos-hidden']) {
    const hiddenValue = value ?? property.default
    return <input type="hidden" name={fieldName} value={hiddenValue === undefined ? '' : typeof hiddenValue === 'object' ? JSON.stringify(hiddenValue) : String(hiddenValue)} readOnly />
  }
  const wide = ['string', 'object', 'array'].includes(property.type ?? 'string') ? 'wide' : ''
	let choices: Array<{value: string; label: string}> | null = null
	const schemaSource = property['x-kubephos-schema-from-application']
	if (schemaSource) {
		const application = applications.find(item => item.reference === formValues[schemaSource])
		const dynamicSchema = application?.descriptor.spec?.valuesSchema
		if (!application || !dynamicSchema) return <div className="dynamic-schema wide"><strong>{title}</strong></div>
		const defaults = application.descriptor.spec?.defaults ?? {}
		const saved = value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
		return <div className="dynamic-schema wide" key={application.reference}><strong>{title}</strong><SchemaFieldSet schema={dynamicSchema} prefix={fieldName} initialValues={schemaValues(dynamicSchema, {...defaults, ...saved}, applications)} applications={applications} artifacts={artifacts} connections={connections} credentials={credentials} /></div>
	}
	if (property.type === 'object' && property.properties) {
		const nestedSchema: JsonSchema = {type: 'object', required: property.required, properties: property.properties}
		const nestedValues = typeof value === 'object' && value && !Array.isArray(value) ? value as Record<string, unknown> : {}
		return <div className="dynamic-schema wide"><strong>{title}</strong><SchemaFieldSet schema={nestedSchema} prefix={fieldName} initialValues={schemaValues(nestedSchema, nestedValues, applications)} applications={applications} artifacts={artifacts} connections={connections} credentials={credentials} /></div>
	}

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
    const selected = String(value ?? property.default ?? choices[0]?.value ?? '')
    const options = selected && !choices.some(choice => choice.value === selected) ? [{value: selected, label: `${selected} · saved value`}, ...choices] : choices
    return <label className={wide}>{title}<select name={fieldName} required={required} disabled={!options.length} defaultValue={selected} onChange={event => onValue(schemaValue(event.target.value, property))}>
      {options.length ? options.map(choice => <option key={choice.value} value={choice.value}>{choice.label}</option>) : <option value="">No compatible value available</option>}
    </select></label>
  }
  if (property.type === 'boolean') {
    return <label className="checkbox-field wide"><input name={fieldName} type="checkbox" defaultChecked={Boolean(value ?? property.default)} onChange={event => onValue(event.target.checked)} />{title}</label>
  }
  if (property.type === 'object' || property.type === 'array') {
    const serialized = value === undefined && property.default === undefined ? '' : JSON.stringify(value ?? property.default, null, 2)
    return <label className="wide">{title}<textarea name={fieldName} rows={4} required={required} defaultValue={serialized} onChange={event => onValue(event.target.value)} /></label>
  }
  if (property.type === 'string' && property['x-kubephos-multiline']) {
    return <label className="wide">{title}<textarea name={fieldName} rows={5} required={required} defaultValue={value === undefined && property.default === undefined ? '' : String(value ?? property.default)} minLength={property.minLength} maxLength={property.maxLength} onChange={event => onValue(event.target.value)} /></label>
  }
  const numeric = property.type === 'integer' || property.type === 'number'
  const descriptionID = property.description ? `${fieldName}-description`.replaceAll('.', '-') : undefined
  return <label className={wide}>{title}<input
    name={fieldName}
    aria-label={title}
    aria-describedby={descriptionID}
    type={numeric ? 'number' : property.writeOnly ? 'password' : 'text'}
    required={required}
    defaultValue={value === undefined && property.default === undefined ? '' : String(value ?? property.default)}
    min={property.minimum}
    max={property.maximum}
    step={numeric ? property.multipleOf ?? (property.type === 'integer' ? 1 : 'any') : undefined}
    minLength={property.minLength}
    maxLength={property.maxLength}
    onChange={event => onValue(schemaValue(event.target.value, property))}
  />{property.description && <small id={descriptionID}>{property.description}</small>}</label>
}

function schemaValues(schema: JsonSchema, values: Record<string, unknown>, applications: Application[]): Record<string, unknown> {
  const result: Record<string, unknown> = {}
  for (const [name, property] of Object.entries(schema.properties ?? {})) {
    if (values[name] !== undefined) result[name] = values[name]
		else if (property.default !== undefined) result[name] = property.default
		else if (property.enum?.length) result[name] = property.enum[0]
		else if (property.format === 'kubephos-application-ref' && applications.length) result[name] = applications[0].reference
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
