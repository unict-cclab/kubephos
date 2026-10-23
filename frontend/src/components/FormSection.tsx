import type {ReactNode} from 'react'

export function FormSection({step, title, children}: {step: number; title: string; children?: ReactNode}) {
  return <div className="form-section console-form-section">
    <div className="console-section-title"><span className="console-section-step" aria-hidden="true">{step}</span><h3>{title}</h3></div>
    {children && <div className="console-section-action">{children}</div>}
  </div>
}
