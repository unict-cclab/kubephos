import type {JsonSchema} from '../types'

export function applicationSettingsSchema(schema: JsonSchema, resources: boolean): JsonSchema {
  const hidden = new Set(['initialReplicas', 'replicaOverrides'])
  const resourceFields = new Set(['cpuRequest', 'cpuLimit', 'memoryRequest', 'memoryLimit', 'resourceOverrides'])
  const properties = Object.fromEntries(Object.entries(schema.properties ?? {}).filter(([name]) => !hidden.has(name) && resourceFields.has(name) === resources).map(([name, property]) => [name, resources ? {...property, 'x-kubephos-advanced': false} : property]))
  return {...schema, properties, required: (schema.required ?? []).filter(name => name in properties)}
}

