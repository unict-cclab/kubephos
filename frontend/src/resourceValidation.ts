// Match the quantity syntax and unit factors accepted by the backend.
function quantity(value:unknown):[bigint,bigint]|undefined {
 if(typeof value!=='string'||!value)return undefined
 const match=/^([0-9]+)(?:\.([0-9]+))?(m|[KMGTPE]i|[kKMGTPE])?$/.exec(value)
 if(!match)return undefined
 const decimals=match[2]??''
 let numerator=BigInt(match[1]+decimals),denominator=10n**BigInt(decimals.length)
 const suffix=match[3]??''
 if(suffix==='m')denominator*=1000n
 else if(suffix){const power=BigInt('KMGTPE'.indexOf(suffix[0].toUpperCase())+1);numerator*=(suffix.endsWith('i')?1024n:1000n)**power}
 return [numerator,denominator]
}
export function resourceFieldError(field:string,values:Record<string,unknown>,inherited:Record<string,unknown>={}):string {
 const value=values[field]
 if(value!==undefined&&value!=='') {
  const parsed=quantity(value)
  if(!parsed)return 'Invalid quantity (e.g. 500m or 512Mi).'
  if(field.endsWith('Limit')&&parsed[0]===0n)return 'Limit must be positive.'
 }
 if(field.endsWith('Limit')) {
  const resource=field.slice(0,-5)
  const request=quantity(values[`${resource}Request`]||inherited[`${resource}Request`])
  const limit=quantity(value||inherited[field])
  if(request&&limit&&request[0]*limit[1]>limit[0]*request[1])return 'Request exceeds limit.'
 }
 return ''
}
