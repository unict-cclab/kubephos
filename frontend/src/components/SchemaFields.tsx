import {standardFieldLabel} from '../fieldLabels'
import {ResourceAction} from './ResourceAction'
import {useEffect, useRef, useState} from 'react'
import type {Application, ApplicationComponent, Artifact, Connection, Credential, JsonSchema, SchemaProperty} from '../types'
import {humanize} from '../lib'
import {ServiceResourceEditor, serviceResourceFields} from './ServiceResourceEditor'

interface SchemaFieldsProps {
  schema: JsonSchema
  layout?: 'network'
  serviceTargets?: ApplicationComponent[]
  onValuesChange?: () => void
  prefix?: string
  values?: Record<string, unknown>
  suggestions?: Record<string, unknown>
  applications: Application[]
  artifacts: Artifact[]
  connections: Connection[]
  credentials: Credential[]
}

export function SchemaFields({schema, prefix = 'schema', values = {}, suggestions, applications, artifacts, connections, credentials, onValuesChange, serviceTargets, layout}: SchemaFieldsProps) {
	const initialValues = schemaValues(schema, values, applications)
  const signature = JSON.stringify({schema, initialValues})
  return <SchemaFieldSet layout={layout} key={signature} schema={schema} prefix={prefix} initialValues={initialValues} suggestions={suggestions} applications={applications} artifacts={artifacts} connections={connections} credentials={credentials} onValuesChange={onValuesChange} serviceTargets={serviceTargets} />
}

interface SchemaFieldSetProps extends Omit<SchemaFieldsProps, 'values'> {
  initialValues: Record<string, unknown>
}

function SchemaFieldSet({schema, prefix = 'schema', initialValues, suggestions, applications, artifacts, connections, credentials, onValuesChange, serviceTargets, layout}: SchemaFieldSetProps) {
  const required = new Set(schema.required ?? [])
  const [currentValues, setCurrentValues] = useState<Record<string, unknown>>(initialValues)
  const touched = useRef(new Set<string>())
  useEffect(() => {
    if (!suggestions) return
    setCurrentValues(current => ({...current, ...Object.fromEntries(Object.entries(suggestions).filter(([key, value]) => value !== undefined && !touched.current.has(key)))}))
  }, [suggestions])
  const resources = serviceResourceFields.every(field => schema.properties?.[field]?.type === 'string')
  const resourceNames = new Set<string>(resources ? serviceResourceFields : [])
  if (resources && serviceTargets !== undefined) resourceNames.add('resourceOverrides')
  const fields = Object.entries(schema.properties ?? {}).filter(([name, property]) => !resourceNames.has(name) && visible(property, currentValues)).sort(([, left], [, right]) => (left['x-kubephos-order'] ?? Number.MAX_SAFE_INTEGER) - (right['x-kubephos-order'] ?? Number.MAX_SAFE_INTEGER))
  const advancedResources = resources && serviceResourceFields.every(field => schema.properties?.[field]?.['x-kubephos-advanced'])
  const renderField = ([name, property]: [string, SchemaProperty]) => <SchemaField
    key={name}
    name={name}
    prefix={prefix}
    property={property}
    value={currentValues[name]}
    formValues={currentValues}
    required={required.has(name)}
    onValue={value => { touched.current.add(name); setCurrentValues(current => ({...current, [name]: value})); onValuesChange?.() }}
    applications={applications}
    artifacts={artifacts}
    connections={connections}
    credentials={credentials}
    onValuesChange={onValuesChange}
    serviceTargets={serviceTargets}
  />
  const resourceEditor = resources && <ServiceResourceEditor prefix={prefix} values={currentValues} targets={serviceTargets} onValue={changes => { setCurrentValues(current => ({...current, ...changes})); onValuesChange?.() }} />
  const advancedFields = fields.filter(([, property]) => property['x-kubephos-advanced'])
  return <div className={`schema-fields${layout ? ` schema-fields-${layout}` : ''}`}>
    {fields.filter(([, property]) => !property['x-kubephos-advanced']).map(renderField)}
    {!advancedResources && resourceEditor}
    {(advancedFields.length > 0 || advancedResources) && <details className="schema-advanced wide">
      <summary>Advanced settings</summary>
      <div className="schema-fields">
        {advancedFields.map(renderField)}
        {advancedResources && resourceEditor}
      </div>
    </details>}
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

function SchemaField({name, prefix, property, value, required, onValue, formValues, applications, artifacts, connections, credentials, onValuesChange, serviceTargets}: SchemaFieldProps) {
  const title = standardFieldLabel(name, property.title) ?? property.title ?? humanize(name)
  const strategyConfig = name === 'configFile' || ['Scheduler configuration', 'Descheduler policy', 'Autoscaler parameters'].includes(property.title ?? '')
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
		return <div className="dynamic-schema wide" key={application.reference}><strong>{title}</strong><SchemaFieldSet schema={dynamicSchema} prefix={fieldName} initialValues={schemaValues(dynamicSchema, {...defaults, ...saved}, applications)} applications={applications} artifacts={artifacts} connections={connections} credentials={credentials} onValuesChange={onValuesChange} serviceTargets={serviceTargets} /></div>
	}
	if (property.type === 'object' && property.properties) {
		const nestedSchema: JsonSchema = {type: 'object', required: property.required, properties: property.properties}
		const nestedValues = typeof value === 'object' && value && !Array.isArray(value) ? value as Record<string, unknown> : {}
		return <div className="dynamic-schema wide"><strong>{title}</strong><SchemaFieldSet schema={nestedSchema} prefix={fieldName} initialValues={schemaValues(nestedSchema, nestedValues, applications)} applications={applications} artifacts={artifacts} connections={connections} credentials={credentials} onValuesChange={onValuesChange} serviceTargets={serviceTargets} /></div>
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
      .map(item => ({value: item.reference, label: item.name}))
  } else if (property.format === 'kubephos-artifact-ref') {
    const type = property['x-kubephos-artifact-type']
    const version = property['x-kubephos-artifact-version']
    choices = artifacts
      .filter(item => (!type || item.type === type) && (!version || item.version === version))
      .map(item => ({value: item.id, label: `${item.name} · ${item.type}/${item.version} · ${item.operationId.slice(0, 12)}…`}))
  } else if (property.enum) {
    choices = property.enum.map(value => ({value: String(value), label: String(value)}))
  }

  if (property.enum?.length === 1 && typeof property.enum[0] !== 'object' && (value === undefined || String(value) === String(property.enum[0]))) {
    return <input type="hidden" name={fieldName} value={String(property.enum[0])} readOnly />
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
  if (property.type === 'array' && property.items?.type === 'string' && property.items.enum?.length) {
    const entries = Array.isArray(value) ? value.map(String) : []
    const options = property.items.enum.map(String)
    const itemTitle = (index: number) => property['x-kubephos-item-prefix'] ? `${property['x-kubephos-item-prefix']}${index + 1}` : `${title} ${index + 1}`
    return <div className="dynamic-schema schema-list wide">
      <strong>{title}</strong>
      <input type="hidden" name={fieldName} value={JSON.stringify(entries)} readOnly />
      {entries.map((entry, index) => <div className="schema-list-row" key={index}>
        <label>{itemTitle(index)}<select aria-label={`Model for ${itemTitle(index)}`} value={entry} onChange={event => onValue(entries.map((current, position) => position === index ? event.target.value : current))}>
          {!options.includes(entry) && <option value={entry}>{entry} · saved value</option>}
          {options.map(option => <option key={option} value={option}>{option}</option>)}
        </select></label>
        <ResourceAction action="remove" label={`Remove ${itemTitle(index)}`} disabled={entries.length <= (property.minItems ?? 0)} onClick={() => onValue(entries.filter((_, position) => position !== index))} />
      </div>)}
      <ResourceAction action="add" label="Add service" disabled={entries.length >= (property.maxItems ?? Infinity)} onClick={() => onValue([...entries, options[0]])} />
    </div>
  }
  if (property.type === 'object' || property.type === 'array') {
    const serialized = value === undefined && property.default === undefined ? '' : JSON.stringify(value ?? property.default, null, 2)
    return <label className="wide">{title}<textarea className={strategyConfig ? 'strategy-config-editor' : 'code-editor'} name={fieldName} rows={strategyConfig ? 20 : 4} required={required} defaultValue={serialized} onChange={event => onValue(event.target.value)} /></label>
  }
  if (property.type === 'string' && property['x-kubephos-multiline']) {
    return <label className="wide">{title}<textarea className={strategyConfig ? 'strategy-config-editor' : undefined} name={fieldName} rows={strategyConfig ? 20 : 5} required={required} defaultValue={value === undefined && property.default === undefined ? '' : String(value ?? property.default)} minLength={property.minLength} maxLength={property.maxLength} onChange={event => onValue(event.target.value)} /></label>
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

// Use the same visibility rules when deciding whether a section has controls.
export function hasConfigurableFields(schema: JsonSchema, values: Record<string, unknown> = {}): boolean {
  const effective = {...Object.fromEntries(Object.entries(schema.properties ?? {}).filter(([, property]) => property.default !== undefined).map(([name, property]) => [name, property.default])), ...values}
  return Object.entries(schema.properties ?? {}).some(([name, property]) => {
    if (property['x-kubephos-hidden'] || !visible(property, effective)) return false
    if (property.enum?.length === 1 && (effective[name] === undefined || String(effective[name]) === String(property.enum[0]))) return false
    if (property.type === 'object' && property.properties) return hasConfigurableFields(property, effective[name] && typeof effective[name] === 'object' ? effective[name] as Record<string, unknown> : {})
    return true
  })
}
