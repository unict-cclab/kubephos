import {useEffect, useMemo, useState} from 'react'
import type {ApplicationComponent} from '../types'

export function StrategyTargetEditor({componentKey, components, trait, resetKey, included = [], excluded = []}: {componentKey: number; components: ApplicationComponent[]; trait: string; resetKey: string; included?: string[]; excluded?: string[]}) {
  const targets = useMemo(() => {
    const explicitDescheduling = components.some(component => component.traits?.includes('deschedulable'))
    return components.filter(component => component.traits?.includes(trait) || trait === 'deschedulable' && !explicitDescheduling && component.traits?.includes('schedulable') && component.traits.includes('scalable') && !component.traits.includes('stateful') && !component.traits.includes('traffic-entrypoint'))
  }, [components, trait])
  const [selected, setSelected] = useState<Record<string, boolean>>({})
  useEffect(() => {
    const restricted = included.length > 0
    setSelected(Object.fromEntries(targets.map(target => [target.id, (!restricted || included.includes(target.id)) && !excluded.includes(target.id)])))
  }, [resetKey, targets.map(target => target.id).join('|')])
  const selectedCount = targets.filter(target => selected[target.id] !== false).length
  const excludedTargets = targets.filter(target => selected[target.id] === false).map(target => target.id)
  return <details className="autoscaler-target-editor strategy-target-editor">
    <summary className="target-editor-heading"><strong>Compatible services</strong><span>{selectedCount} / {targets.length}</span></summary>
    <input type="hidden" name={`include-${componentKey}`} value="" readOnly />
    <input type="hidden" name={`exclude-${componentKey}`} value={excludedTargets.join(',')} readOnly />
    <div className="target-editor-list">{targets.map(target => {
      const enabled = selected[target.id] !== false
      return <article className={enabled ? 'enabled' : ''} key={target.id}>
        <div className="target-editor-row"><label className="checkbox-field"><input type="checkbox" checked={enabled} onChange={event => setSelected(current => ({...current, [target.id]: event.target.checked}))} /><span>{target.id}</span></label></div>
      </article>
    })}</div>
    {!targets.length && <p className="details-copy">The application contract does not declare compatible services.</p>}
  </details>
}
