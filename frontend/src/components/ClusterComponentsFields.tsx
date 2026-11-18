import {useEffect, useMemo, useState} from 'react'
import {resolveComponentSelection, useClusterComponentCatalog} from '../clusterComponents'
import {ErrorFeedback} from './ToastRegion'

// Selects the components installed on a cluster. The Default preset is the catalog's
// complete set; changing a component switches to Custom. Core components are always
// installed. The selection is submitted as repeated "components" fields; without the
// catalog no field is submitted, so the server installs its complete default set.
export function ClusterComponentsFields({networking, initial, onChange}: {networking: 'flannel' | 'cilium'; initial?: string[]; onChange?: (components: string[]) => void}) {
  const {catalog, error} = useClusterComponentCatalog()
  const [custom, setCustom] = useState(initial)
  const selected = useMemo(() => !catalog ? [] : custom ? resolveComponentSelection(catalog.items, custom, networking) : catalog.defaults[networking], [catalog, custom, networking])
  useEffect(() => { if (catalog) onChange?.(selected) }, [catalog, selected, onChange])
  if (error) return <ErrorFeedback message={error}/>
  if (!catalog) return <p className="field-hint">Loading cluster components…</p>
  const available = catalog.items.filter(component => !(component.unsupportedNetworking ?? []).includes(networking))
  const toggle = (id: string, checked: boolean) => setCustom(checked ? [...selected, id] : selected.filter(value => value !== id))
  return <>
    <label className="field-short">Preset<select value={custom ? 'custom' : 'default'} onChange={event => setCustom(event.target.value === 'custom' ? selected : undefined)}><option value="default">Default</option><option value="custom">Custom</option></select></label>
    <div className="cluster-component-list">{available.map(component => {
      const unmet = Boolean(component.requiresAny?.length) && !component.requiresAny!.some(id => selected.includes(id))
      return <label className="checkbox-field" key={component.id}><input type="checkbox" checked={selected.includes(component.id)} disabled={component.core || unmet} onChange={event => toggle(component.id, event.target.checked)} /><span>{component.title}<small>{component.core ? 'Always installed. ' : ''}{component.description}</small></span></label>
    })}</div>
    {selected.map(id => <input key={id} type="hidden" name="components" value={id} />)}
  </>
}
