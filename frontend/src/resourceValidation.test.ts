import {expect,it} from 'vitest'
import {resourceFieldError} from './resourceValidation'
it('keeps resources optional, accepts zero requests and rejects zero limits and invalid quantities',()=>{
 expect(resourceFieldError('cpuRequest',{})).toBe('')
 expect(resourceFieldError('cpuRequest',{cpuRequest:'0'})).toBe('')
 expect(resourceFieldError('cpuLimit',{cpuLimit:'0m'})).toBe('Limit must be positive.')
 for(const value of ['-1','NaN','1GB','1e3'])expect(resourceFieldError('memoryRequest',{memoryRequest:value})).not.toBe('')
})
it('compares decimal, milli and binary quantities including inherited service values',()=>{
 expect(resourceFieldError('cpuLimit',{cpuRequest:'1000m',cpuLimit:'1'})).toBe('')
 expect(resourceFieldError('memoryLimit',{memoryRequest:'1024Mi',memoryLimit:'1Gi'})).toBe('')
 expect(resourceFieldError('memoryLimit',{memoryLimit:'1Gi'},{memoryRequest:'1025Mi'})).toBe('Request exceeds limit.')
 expect(resourceFieldError('cpuLimit',{cpuRequest:'0.6'},{cpuLimit:'500m'})).toBe('Request exceeds limit.')
})
