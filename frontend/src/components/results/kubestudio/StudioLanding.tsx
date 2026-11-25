import {useId, useMemo, useState} from 'react'
import type {OverviewGroup} from '../overview'
import type {StudioData} from './image'
import {customForms, KindIcon, type CustomForm} from './recipes'
import {buildSuggestions, goals, type Goal, type Suggestion} from './suggestions'

// KubeStudio's first screen for a new figure: what to show (goal chips that filter), the charts
// suggested by this experiment's results, each with its reason, then the editor on one click;
// plus a custom chart and the last figure for those who know what they want.

const shownAtOnce = 6

export function StudioLanding({data, groups, forms, last, onPick, onCustom, onContinue}: {data: StudioData; groups: OverviewGroup[]; forms: CustomForm[]; last?: {name: string; charts: number}; onPick: (suggestion: Suggestion) => void; onCustom: (form: CustomForm) => void; onContinue: () => void}) {
  const id = useId()
  const suggestions = useMemo(() => buildSuggestions(data, groups), [data, groups])
  const [goal, setGoal] = useState<Goal>()
  const [more, setMore] = useState(false)
  const offered = goals.filter(item => suggestions.some(suggestion => suggestion.goal === item.id))
  const matching = goal ? suggestions.filter(item => item.goal === goal) : suggestions
  const listed = more || goal ? matching : matching.slice(0, shownAtOnce)
  return <div className="ks-landing">
    <div className="ks-landing-grid">
      <section className="ks-landing-main" aria-labelledby={`${id}-goal`}>
        <h3 id={`${id}-goal`}>What do you want to show?</h3>
        {offered.length > 1 && <div className="ks-goals" role="group" aria-labelledby={`${id}-goal`}>
          <button type="button" aria-pressed={!goal} onClick={() => setGoal(undefined)}>Everything</button>
          {offered.map(item => <button key={item.id} type="button" aria-pressed={goal === item.id} title={item.question} onClick={() => setGoal(goal === item.id ? undefined : item.id)}>{item.label}</button>)}
        </div>}
        <h3 id={`${id}-list`} className="ks-landing-sub">Suggested for this experiment</h3>
        {listed.length ? <ol className="ks-suggestions" aria-labelledby={`${id}-list`}>
          {listed.map((suggestion, index) => <li key={suggestion.id}>
            <button type="button" className="ks-suggestion" data-suggestion={suggestion.id} data-kind={suggestion.chart.kind} onClick={() => onPick(suggestion)}>
              <span className="ks-suggestion-thumb">{suggestion.thumb}</span>
              <span className="ks-suggestion-text">
                <strong>{!goal && index === 0 && <span className="ks-suggestion-best">Best match</span>}{!goal && index === 0 && ' '}{suggestion.title}</strong>
                <small className="ks-suggestion-why" data-severity={suggestion.severity}>{suggestion.why}</small>
              </span>
            </button>
          </li>)}
        </ol> : <p className="ks-hint">No ready-made chart fits these results. Start from a custom chart.</p>}
        {listed.length > 0 && <p className="ks-landing-note">Pick one to open it in the editor — you can change data, style and layout there.</p>}
        {!goal && !more && matching.length > shownAtOnce && <button type="button" className="ks-link" onClick={() => setMore(true)}>Show {matching.length - shownAtOnce} more</button>}
      </section>
      <aside className="ks-landing-side" aria-label="Other ways to start">
        {last && <section>
          <h3>Continue</h3>
          <button type="button" className="ks-continue" onClick={onContinue}><strong>Continue last figure</strong><small>{last.name}{last.charts > 1 ? ` · ${last.charts} charts` : ''}</small></button>
        </section>}
        {forms.length > 0 && <section>
          <h3>Custom chart</h3>
          <p className="ks-hint">Pick the form, then choose X and Y freely.</p>
          <div className="ks-custom-forms">
            {customForms.filter(form => forms.includes(form.kind)).map(form => <button key={form.kind} type="button" className="ks-custom-form" data-form={form.kind} onClick={() => onCustom(form.kind)}>
              <KindIcon kind={form.kind} /><span><strong>{form.label}</strong><small>{form.hint}</small></span>
            </button>)}
          </div>
        </section>}
      </aside>
    </div>
  </div>
}
