import {useId,useRef,useState,type InputHTMLAttributes,type Ref} from 'react'

// Keep validation next to its control, while native validity still blocks submit.
export function ValidatedInput({error='',inputRef,onChange,onBlur,onInvalid,pattern,...props}:InputHTMLAttributes<HTMLInputElement>&{error?:string;inputRef?:Ref<HTMLInputElement>}) {
 const id=useId()
 const edited=useRef(false)
 const [nativeError,setNativeError]=useState('')
 const message=props.disabled?'':error||nativeError
 const sync=(input:HTMLInputElement)=>input.setCustomValidity(error||(props.required&&(!props.type||props.type==='text')&&input.value&&!input.value.trim()?'Required.':''))
 const report=(input:HTMLInputElement)=>{
  if(error)return ''
  const state=input.validity
  if(state.valueMissing)return 'Required.'
  if(state.rangeUnderflow)return `Minimum: ${input.min}.`
  if(state.rangeOverflow)return `Maximum: ${input.max}.`
  if(state.stepMismatch)return input.step==='1'?'Use a whole number.':`Use increments of ${input.step}.`
  if(state.patternMismatch)return 'Invalid format.'
  if(state.tooShort)return `At least ${input.minLength} characters.`
  if(state.tooLong)return `Up to ${input.maxLength} characters.`
  if(state.typeMismatch)return input.type==='email'?'Invalid email address.':'Invalid value.'
  return input.validationMessage
 }
 return <><input {...props} pattern={pattern?.replace(/(?<!\\)-(?=\])/g,'\\-')} ref={input=>{if(input)sync(input);if(typeof inputRef==='function')inputRef(input);else if(inputRef)inputRef.current=input}} aria-invalid={message?true:undefined} aria-describedby={[props['aria-describedby'],message?id:undefined].filter(Boolean).join(' ')||undefined} onChange={event=>{edited.current=true;sync(event.currentTarget);setNativeError(event.currentTarget.value?report(event.currentTarget):'');onChange?.(event)}} onBlur={event=>{if(edited.current||event.currentTarget.value)setNativeError(report(event.currentTarget));onBlur?.(event)}} onInvalid={event=>{setNativeError(report(event.currentTarget));onInvalid?.(event)}}/>{message&&<small id={id} className="field-validation-error" aria-live="polite">{message}</small>}</>
}
