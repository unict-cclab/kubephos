import {useEffect, useRef, useState} from 'react'
import {copyText} from '../clipboard'
import {ErrorFeedback, useErrorState} from './ToastRegion'

export function CopyButton({value, label = 'Copy code'}: {value: string; label?: string}) {
  const [copied, setCopied] = useState(false)
  const [pending, setPending] = useState(false)
  const [error, setError] = useErrorState()
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const mounted = useRef(false)
  const generation = useRef(0)
  useEffect(() => {
    mounted.current = true
    generation.current++
    setCopied(false)
    setPending(false)
    return () => {mounted.current = false; clearTimeout(timer.current)}
  }, [value])
  const copy = async () => {
    const current = generation.current
    setPending(true); setError('')
    try {
      await copyText(value)
      if (!mounted.current || generation.current !== current) return
      setCopied(true); clearTimeout(timer.current)
      timer.current = setTimeout(() => setCopied(false), 1800)
    } catch {if (mounted.current && generation.current === current) setError('Copy was blocked by the browser. Select the code and copy it manually.')}
    finally {if (mounted.current && generation.current === current) setPending(false)}
  }
  return <><button type="button" className="button secondary compact" aria-label={label} disabled={pending || !value} onClick={() => void copy()}>{copied ? 'Copied' : 'Copy'}</button><span className="code-copy-status" role="status">{copied ? 'Code copied' : ''}</span><ErrorFeedback message={error} /></>
}

export function CodeBlock({children, label = 'Code snippet', className}: {children: string; label?: string; className?: string}) {
  return <div className="code-block"><div className="code-block-actions"><CopyButton value={children} label={`Copy ${label}`} /></div><pre className={className} aria-label={label}><code>{children}</code></pre></div>
}
