import {useEffect, useRef, useState} from 'react'
import {request} from '../api'

type Suggestions = {vmid?: number; address?: string; prefixLength?: number; gateway?: string; dnsServer?: string}
type Field = keyof Suggestions

export function useProvisioningSuggestions(open: boolean, connectionID: string, templateID = '', count = 1) {
  const [values, setValues] = useState<Suggestions>({})
  const touched = useRef(new Set<Field>())
  useEffect(() => {
    touched.current.clear()
    setValues({})
  }, [open, connectionID])
  useEffect(() => {
    if (!open || !connectionID || count < 1 || count > 108) return
    const controller = new AbortController()
    const query = new URLSearchParams({count: String(count)})
    if (templateID) query.set('templateId', templateID)
    request<Suggestions>(`/connections/${encodeURIComponent(connectionID)}/provisioning-suggestions?${query}`, {}, undefined, controller.signal)
      .then(suggestions => {
        if (controller.signal.aborted) return
        setValues(current => {
          const next = {...current}
          for (const key of ['vmid', 'address', 'prefixLength', 'gateway', 'dnsServer'] as const) {
            if (!touched.current.has(key) && suggestions[key] !== undefined) Object.assign(next, {[key]: suggestions[key]})
          }
          return next
        })
      })
      .catch(() => {}) // Forms remain usable with manually entered values.
    return () => controller.abort()
  }, [open, connectionID, templateID, count])
  const field = (name: Field, fallback: string | number = '') => ({
    value: values[name] ?? fallback,
    onChange: (event: {target: {value: string}}) => {
      touched.current.add(name)
      setValues(current => ({...current, [name]: event.target.value}))
    }
  })
  return {values, field}
}
