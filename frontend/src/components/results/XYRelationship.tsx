import {useState} from 'react'
import type {OverviewGroup} from './overview'
import {XYChart, XYControls} from './XYChart'
import {defaultXYOptions, defaultXYSelection, useXYData, type XYSource, type XYState} from './xy'

// Two metrics against each other (e.g. achieved req/s vs client P95), on demand.
export default function XYRelationship({source, groups, onFigure, onClose}: {source: XYSource; groups: OverviewGroup[]; onFigure?: (state: XYState) => void; onClose: () => void}) {
  const [state, setState] = useState<XYState>(() => ({...defaultXYSelection(source.metrics), options: defaultXYOptions}))
  const data = useXYData(source, state)
  return <section className="results-card metric-card-mini xy-card" aria-label="X–Y relationship">
    <div className="results-card-head">
      <div><h4>X–Y relationship</h4><p>Each point is one sampling window, X and Y taken at the same instant.</p></div>
      <div className="results-card-actions">
        {onFigure && <button type="button" className="link-button" onClick={() => onFigure(state)}>Create figure</button>}
        <button type="button" className="link-button" aria-expanded={true} onClick={onClose}>Close</button>
      </div>
    </div>
    <XYControls source={source} value={state} onChange={setState} />
    {data && <XYChart data={data} options={state.options} groups={groups} />}
  </section>
}
