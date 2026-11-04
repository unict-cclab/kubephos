import {useEffect, useRef, useState, type FocusEvent, type InputHTMLAttributes} from 'react'

type Props = Omit<InputHTMLAttributes<HTMLInputElement>, 'type' | 'value' | 'defaultValue' | 'onChange'> & {
  value: number
  validationMessage?: string
  onValueChange: (value: number) => void
}

export function NumericInput({value, onValueChange, onBlur, validationMessage, ...props}: Props) {
  const input = useRef<HTMLInputElement>(null)
  useEffect(() => {input.current?.setCustomValidity(validationMessage ?? '')}, [validationMessage])
  const [draft, setDraft] = useState(String(value))

  useEffect(() => setDraft(String(value)), [value])

  const blur = (event: FocusEvent<HTMLInputElement>) => {
    if (event.currentTarget.value === '' || !Number.isFinite(event.currentTarget.valueAsNumber)) setDraft(String(value))
    onBlur?.(event)
  }

  return <input {...props} ref={input} type="number" value={draft} onChange={event => {
    const next = event.currentTarget.value
    setDraft(next)
    if (next !== '' && Number.isFinite(event.currentTarget.valueAsNumber)) onValueChange(event.currentTarget.valueAsNumber)
  }} onBlur={blur} />
}
