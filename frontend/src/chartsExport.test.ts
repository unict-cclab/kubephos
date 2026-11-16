import {afterEach, expect, it, vi} from 'vitest'
import {exportChart, svgToPDF, svgToPDFAsync} from './charts'

afterEach(()=>{document.body.replaceChildren();vi.useRealTimers()})
function chart(count=250) {
 const svg=document.createElementNS('http://www.w3.org/2000/svg','svg')
 svg.id='plot';svg.setAttribute('viewBox','0 0 860 680')
 svg.innerHTML='<text x="10" y="20">Original figure</text>'+Array.from({length:count},(_,i)=>`<rect x="${i}" y="30" width="1" height="100" fill="#08306b"/>`).join('')
 document.body.append(svg)
 return svg
}
it('allows other browser tasks to run during PDF generation and preserves the exact vector output',async()=>{
 const svg=chart()
 let responsive=false
 setTimeout(()=>{responsive=true},0)
 const output=await svgToPDFAsync(svg)
 expect(responsive).toBe(true)
 expect(output).toEqual(svgToPDF(svg))
})
it('exports an immutable snapshot after giving the loader time to paint',async()=>{
 vi.useFakeTimers()
 const svg=chart()
 const expected=svgToPDF(svg)
 const ready=vi.fn(async()=>{})
 const task=exportChart('plot','cpu','pdf',ready)
 expect(ready).not.toHaveBeenCalled()
 svg.querySelector('text')!.textContent='Changed figure'
 svg.remove()
 await vi.runAllTimersAsync()
 await task
 expect(ready).toHaveBeenCalledExactlyOnceWith(expected)
})
it('reports missing plots instead of silently ending the download',async()=>{
 await expect(exportChart('missing','cpu','pdf')).rejects.toThrow('The plot is no longer available.')
})
