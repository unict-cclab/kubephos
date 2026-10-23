import {ReadoutTable, type ReadoutTableData} from './ReadoutTable'
import {Children, Fragment, createContext, useContext, useEffect, forwardRef, isValidElement, useId, useRef, useState, type SubmitEvent, type FormHTMLAttributes, type ReactElement, type ReactNode} from 'react'
import {ActionIcon} from './ActionIcon'

const WizardContext = createContext<{form: HTMLFormElement | null; step: number}>({form: null, step: 0})

export function WizardReview() {
  const {form, step} = useContext(WizardContext)
  const [groups, setGroups] = useState<{title: string; sections: {title: string; fields: [string, string][]; table?: ReadoutTableData}[]}[]>([])
  useEffect(() => {
    if (!form) return
    setGroups([...form.querySelectorAll<HTMLElement>('[data-wizard-panel]')].flatMap(panel => {
      if (panel.contains(form.querySelector('.wizard-review'))) return []
      const sections = new Map<string, {title: string; fields: [string, string][]; table?: ReadoutTableData}>()
      panel.querySelectorAll<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>('input, select, textarea').forEach(control => {
        if (control.matches(':disabled') || control.closest('[data-review-skip=true]')) return
        if (control.dataset.reviewTable) {
          try {
            const data = JSON.parse(control.dataset.reviewTable) as ReadoutTableData
            const title = control.dataset.reviewTitle ?? ''
            if (Array.isArray(data.headers) && Array.isArray(data.rows) && data.rows.length) sections.set(title, {title, fields: [], table: data})
          } catch { /* A malformed optional preview must not break form review. */ }
          return
        }
        if (control.type === 'hidden' || ['button','submit','file'].includes(control.type)) return
        if ((control.type === 'checkbox' || control.type === 'radio') && !(control as HTMLInputElement).checked) return
        const label = control.dataset.reviewLabel || control.getAttribute('aria-label') || reviewLabel(control)
        if (!label || /password|token|secret|private key|credential|api.?key|access.?key|kubeconfig/i.test(label) || control.type === 'password') return
        const tab = control.closest('[role=tabpanel]')?.getAttribute('aria-labelledby')
        const context = [tab ? document.getElementById(tab)?.textContent ?? '' : '', (control.closest('[data-review-group]') as HTMLElement | null)?.dataset.reviewGroup ?? ''].filter(Boolean).join(' · ')
        const value = control instanceof HTMLSelectElement ? control.selectedOptions[0]?.textContent ?? '' : control.type === 'checkbox' ? 'Enabled' : control.value
        if (!value || value.length > 120 || control instanceof HTMLTextAreaElement) return
        const section = sections.get(context) ?? {title: context, fields: []}
        section.fields.push([label, value])
        sections.set(context, section)
      })
      return sections.size ? [{title: panel.querySelector('h3')?.textContent ?? '', sections: [...sections.values()]}] : []
    }))
  }, [form, step])
  return <div className="wizard-review">{groups.map(group => <section className="wizard-review-section" key={group.title}><h4>{group.title}</h4>{group.sections.map((section, index) => <div className="wizard-review-group" key={index}>{section.title && section.title !== group.title && <h5>{section.title}</h5>}{section.table ? <ReadoutTable data={section.table} label={section.title} /> : <dl>{section.fields.map(([label,value],position) => <div key={position}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>}</div>)}</section>)}</div>
}

function reviewLabel(control: HTMLElement): string {
  const label = control.closest('label')?.cloneNode(true) as HTMLElement | undefined
  if (!label) return ''
  label.querySelectorAll('input, select, textarea, button, small, svg, [hidden]').forEach(node => node.remove())
  return label.textContent?.replace(/\s+/g, ' ').trim() ?? ''
}

type WizardStepProps = {title: string; children: ReactNode; blocked?: boolean; status?: {message: string; error?: boolean}}

export function WizardStep({children}: WizardStepProps) {return <>{children}</>}

type Props = FormHTMLAttributes<HTMLFormElement> & {children: ReactNode; pending?: boolean; submitLabel: string; submitDisabled?: boolean; onCancel: () => void; error?: string}

// Keep every step mounted: dynamic schema values and FormData must survive navigation.
export const WizardForm = forwardRef<HTMLFormElement, Props>(function WizardForm({children, pending = false, submitLabel, submitDisabled, onCancel, error, onSubmit, ...props}, forwardedRef) {
  const formRef = useRef<HTMLFormElement>(null)
  const [active, setActive] = useState(0)
  const id = useId()
  const steps: ReactElement<WizardStepProps>[] = []
  const collect = (nodes: ReactNode) => Children.forEach(nodes, child => {
    if (!isValidElement(child)) return
    if (child.type === Fragment) collect((child.props as {children: ReactNode}).children)
    else if (child.type === WizardStep) steps.push(child as ReactElement<WizardStepProps>)
  })
  collect(children)
  const current = Math.min(active, Math.max(0, steps.length - 1))
  const move = (next: number, invalid?: HTMLElement) => {
    setActive(next)
    requestAnimationFrame(() => {
      const target = invalid ?? formRef.current?.querySelector<HTMLElement>(`[data-wizard-panel="${next}"] h3`)
      target?.focus({preventScroll: true})
      formRef.current?.closest('.editor-page')?.scrollIntoView?.({block: 'start', behavior: 'instant'})
      if (invalid && 'reportValidity' in invalid) (invalid as HTMLInputElement).reportValidity()
    })
  }
  const validate = (all: boolean) => {
    const controls = formRef.current?.querySelectorAll<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>(all ? 'input, select, textarea' : `[data-wizard-panel="${current}"] input, [data-wizard-panel="${current}"] select, [data-wizard-panel="${current}"] textarea`)
    const invalid = [...(controls ?? [])].find(control => control.willValidate && !control.validity.valid)
    if (!invalid) {
      const blockedStep = all ? steps.findIndex(step => step.props.blocked) : -1
      if (blockedStep >= 0) {move(blockedStep); return false}
      return true
    }
    const panel = Number(invalid.closest<HTMLElement>('[data-wizard-panel]')?.dataset.wizardPanel ?? current)
    const ancestors: HTMLElement[] = []
    for (let parent = invalid.parentElement; parent && parent !== formRef.current; parent = parent.parentElement) ancestors.push(parent)
    ancestors.reverse().forEach(parent => {
      if (parent instanceof HTMLDetailsElement) parent.open = true
      if (parent.matches('[role=tabpanel]') && parent.hidden) formRef.current?.querySelector<HTMLButtonElement>(`[aria-controls="${parent.id}"]`)?.click()
    })
    move(panel, invalid)
    return false
  }
  const blocked = Boolean(steps[current]?.props.blocked)
  const stepDisabled = (_index: number) => pending
  const chooseStep = (index: number) => {
    if (!stepDisabled(index)) move(index)
  }
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (pending) return
    if (current < steps.length - 1) {if (!blocked && validate(false)) move(current + 1); return}
    if (validate(true)) onSubmit?.(event)
  }
  return <form {...props} noValidate ref={node => {formRef.current = node; if (typeof forwardedRef === 'function') forwardedRef(node); else if (forwardedRef) forwardedRef.current = node}} onSubmit={submit} onKeyDown={event => {
    props.onKeyDown?.(event)
    if (event.defaultPrevented || event.key !== 'Enter' || event.nativeEvent.isComposing || event.altKey || event.ctrlKey || event.metaKey) return
    const target = event.target
    if (!(target instanceof HTMLInputElement) || ['button', 'submit', 'checkbox', 'radio', 'file'].includes(target.type)) return
    // A wizard has several mounted inputs but no submit button before its final step.
    // Native implicit submission therefore does not reliably advance on Enter.
    event.preventDefault()
    event.currentTarget.requestSubmit()
  }} className={`wizard-form ${props.className ?? ''}`}>
    <nav className="wizard-progress" aria-label="Setup steps"><label className="wizard-step-picker"><span>Step {current + 1} of {steps.length}</span><select aria-label="Setup step" value={current} disabled={pending} onChange={event => chooseStep(Number(event.target.value))}>{steps.map((step, index) => <option key={step.key === null ? `step-${index}` : `key-${step.key}`} value={index} disabled={stepDisabled(index)}>{step.props.title}</option>)}</select></label><ol>{steps.map((step, index) => <li key={step.key === null ? `step-${index}` : `key-${step.key}`}><button type="button" aria-current={index === current ? 'step' : undefined} disabled={stepDisabled(index)} onClick={() => chooseStep(index)}><span className="wizard-step-number" aria-hidden="true">{index < current ? <ActionIcon name="check" /> : index + 1}</span><span>{step.props.title}</span></button></li>)}</ol></nav>
    <WizardContext.Provider value={{form: formRef.current, step: current}}><fieldset className="wizard-panels" disabled={pending}>{steps.map((step, index) => <section key={step.key === null ? `step-${index}` : `key-${step.key}`} data-wizard-panel={index} hidden={index !== current} aria-labelledby={`${id}-${index}`}><div className="wizard-panel-heading"><span className="wizard-position">{index + 1} / {steps.length}</span><h3 id={`${id}-${index}`} tabIndex={-1}>{step.props.title}</h3></div>{step.props.status && <div className={`wizard-step-status${step.props.status.error ? ' invalid' : ''}`} role={step.props.status.error ? 'alert' : 'status'}>{step.props.status.message}</div>}{step.props.children}</section>)}</fieldset></WizardContext.Provider>
    {error && <p className="form-error" role="alert">{error}</p>}
    <div className="wizard-actions"><button className="button secondary" type="button" disabled={pending} onClick={onCancel}>Cancel</button><div>{current > 0 && <button className="button secondary" type="button" disabled={pending} onClick={() => move(current - 1)}><ActionIcon name="left" />Previous</button>}{current < steps.length - 1 ? <button key="continue" className="button primary" type="button" disabled={pending || blocked} onClick={event => {event.preventDefault(); if (validate(false)) move(current + 1)}}>Continue<ActionIcon name="right" /></button> : <button key="submit" className="button primary" type="submit" disabled={pending || submitDisabled}>{pending ? 'Saving…' : submitLabel}</button>}</div></div>
  </form>
})
