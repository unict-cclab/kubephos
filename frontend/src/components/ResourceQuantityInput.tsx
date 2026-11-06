import {useState} from 'react'
import {ValidatedInput} from './ValidatedInput'

const cpuUnits=[['m','Millicores'],['','Cores']]
const memoryUnits=[['','Bytes'],['Ki','Kibibytes'],['Mi','Mebibytes'],['Gi','Gibibytes'],['Ti','Tebibytes'],['Pi','Pebibytes'],['Ei','Exbibytes'],['k','Kilobytes'],['M','Megabytes'],['G','Gigabytes'],['T','Terabytes'],['P','Petabytes'],['E','Exabytes']]
function parts(value:string) {const match=/^([0-9]+(?:\.[0-9]*)?)(.*)$/.exec(value);return {amount:match?.[1]??value,unit:match?.[2]??''}}
function factor(unit:string):[bigint,bigint] {if(unit==='m')return [1n,1000n];if(!unit)return [1n,1n];return [(unit.endsWith('i')?1024n:1000n)**BigInt('KMGTPE'.indexOf(unit[0].toUpperCase())+1),1n]}
export function convertResourceUnit(value:string,unit:string) {
 const previous=parts(value)
 if(!/^\d+(\.\d+)?$/.test(previous.amount))return value
 const [whole,decimal='']=previous.amount.split('.')
 const [a,b]=factor(previous.unit),[c,d]=factor(unit)
 const numerator=BigInt(whole+decimal)*a*d,denominator=10n**BigInt(decimal.length)*b*c
 let result=String(numerator/denominator),remainder=numerator%denominator
 if(remainder){result+='.';for(let i=0;remainder&&i<80;i++){remainder*=10n;result+=String(remainder/denominator);remainder%=denominator}}
 return result+unit
}
export function ResourceQuantityInput({value,name,label,kind,error,placeholder,onChange}:{value:string;name?:string;label:string;kind:'cpu'|'memory';error?:string;placeholder?:string;onChange:(value:string)=>void}) {
 const parsed=parts(value)
 const [emptyUnit,setEmptyUnit]=useState(kind==='cpu'?'m':'Mi')
 const unit=value?parsed.unit:emptyUnit
 const units=kind==='cpu'?cpuUnits:memoryUnits
 return <div className="resource-quantity"><input type="hidden" name={name} value={value} readOnly data-review-title={label}/><div className="resource-quantity-value" data-review-skip="true"><ValidatedInput aria-label={label} inputMode="decimal" value={parsed.amount} error={error} placeholder={placeholder??'Not set'} onChange={event=>{const text=event.target.value;const pasted=parts(text);setEmptyUnit(unit);onChange(text?(pasted.unit?text:text+unit):'')}}/></div><select data-review-skip="true" aria-label={`${label} unit`} value={unit} onChange={event=>{setEmptyUnit(event.target.value);if(value)onChange(convertResourceUnit(value,event.target.value))}}>{!units.some(([key])=>key===unit)&&<option value={unit}>{unit||'Base unit'}</option>}{units.map(([key,title])=><option key={key} value={key}>{title}</option>)}</select></div>
}
