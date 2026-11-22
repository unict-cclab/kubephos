import type {CatalogStrategy, JsonSchema, Plugin} from './types'

// Catalog strategies are run by a plugin of their kind. Image-based plugins, such as the
// Custom Pod Autoscaler, run the entry's image; built-in ones, such as the Kubernetes HPA,
// take only the entry's settings.

export function strategyPlugins(plugins: Plugin[], kind: CatalogStrategy['kind']): Plugin[] {
  return plugins.filter(plugin => plugin.capabilities?.includes(`${kind}.kubernetes.install`))
}

export function runsStrategyImage(plugin: Plugin | undefined): boolean {
  return Object.values(plugin?.schema.properties ?? {}).some(property => property.format === 'kubephos-strategy-image')
}

// strategySettingsSchema keeps the settings a catalog entry can default: references,
// images and per-microservice overrides are chosen in each experiment.
export function strategySettingsSchema(plugin: Plugin): JsonSchema {
  const properties = Object.fromEntries(Object.entries(plugin.schema.properties ?? {}).filter(([name, property]) => name !== 'targetOverrides' && !property.format?.startsWith('kubephos-')))
  return {type: 'object', properties, required: (plugin.schema.required ?? []).filter(name => name in properties)}
}
