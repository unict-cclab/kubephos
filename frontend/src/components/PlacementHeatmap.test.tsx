import {cleanup, render, screen} from '@testing-library/react'
import {afterEach, expect, it} from 'vitest'
import {PlacementHeatmap, zonePlacement, aggregateZonePlacement, zoneColorBlocks, placementColorScale, placementCellColor} from './PlacementHeatmap'
import type {PlacementHistory, TimeSeriesDataset} from '../types'
import {svgToPDF} from '../charts'

afterEach(cleanup)
it('uses distinct discrete bands with exact boundaries for integer and averaged counts',()=>{
 const scale=placementColorScale(22)
 expect(scale.map(b=>b.upper)).toEqual([0,6,12,18,24])
 expect(new Set(scale.map(b=>b.color)).size).toBe(5)
 expect(placementCellColor(0,scale)).toBe(scale[0].color)
 expect(placementCellColor(0.1,scale)).toBe(scale[1].color)
 expect(placementCellColor(6,scale)).toBe(scale[1].color)
 expect(placementCellColor(6.1,scale)).toBe(scale[2].color)
 expect(placementCellColor(22,scale)).toBe(scale[4].color)
 expect(placementColorScale(1).map(b=>b.upper)).toEqual([0,1])
})
const start = Date.parse('2026-11-15T10:00:00Z')
function fixture(): PlacementHistory {
  return {status:'available',source:'test',stepSeconds:15,nodes:[{name:'n1',cluster:'a',zone:'edge'},{name:'n2',cluster:'a',zone:'edge'},{name:'n3',cluster:'a',zone:'cloud'}],snapshots:[
    {timestamp:new Date(start).toISOString(),observed:true,pods:[{uid:'p1',name:'p1',service:'s1',node:'n1',cluster:'a'},{uid:'p2',name:'p2',service:'s2',node:'n2',cluster:'a'},{uid:'p1',name:'p1',service:'s1',node:'n1',cluster:'a'}]},
    {timestamp:new Date(start+15000).toISOString(),observed:false,pods:[]},
    {timestamp:new Date(start+45000).toISOString(),observed:true,pods:[]},
  ]}
}
it('sums all services and nodes per zone, counting each pod UID once',()=>{
 const d=zonePlacement(fixture(),start,start+60000)
 const edge=d.rows.find(r=>r.label==='edge')!
 expect(d.cells[0].counts.get(edge.id)).toBe(2)
 expect(d.maximum).toBe(2)
 expect(d.rows.map(r=>r.label)).toEqual(['cloud','edge'])
})
it('preserves missing samples and temporal gaps without filling them with zero or carrying counts forward',()=>{
 const d=zonePlacement(fixture(),start,start+60000)
 expect(d.cells[1].observed).toBe(false)
 expect(d.cells[1].to).toBeLessThan(d.cells[2].from)
 expect(d.cells[2].observed).toBe(true)
 expect(d.cells[2].counts.size).toBe(0)
})
it('merges equal neighboring colors without merging across missing samples or real changes',()=>{
 const cell=(from:number,to:number,count:number,observed=true)=>({time:from,from,to,observed,counts:new Map([['zone',count]])})
 expect(zoneColorBlocks([cell(0,15,2),cell(15,30,2),cell(30,45,1),cell(45,60,0,false),cell(60,75,1)],'zone')).toEqual([
  {time:0,from:0,to:30,count:2},{time:30,from:30,to:45,count:1},{time:60,from:60,to:75,count:1},
 ])
})
it('keeps identically named zones in different clusters separate and retains unknown-node pods',()=>{
 const h=fixture()
 h.nodes.push({name:'n1',cluster:'b',zone:'edge'})
 h.snapshots[0].pods.push({uid:'p1',name:'p1',service:'s1',node:'n1',cluster:'b'},{uid:'unknown',name:'unknown',service:'s3',node:'unknown',cluster:'b'})
 const d=zonePlacement(h,start,start+60000)
 expect(d.rows.map(r=>r.label)).toEqual(['a · cloud','a · edge','b · edge','b · Unassigned zone'])
 expect([...d.cells[0].counts.values()].reduce((a,b)=>a+b,0)).toBe(4)
})
it('averages each run equally on elapsed workload time and excludes unavailable windows',()=>{
 const a=fixture(), b=fixture()
 b.snapshots=b.snapshots.map(s=>({...s,timestamp:new Date(Date.parse(s.timestamp)+3600000).toISOString(),pods:s.pods.filter(p=>p.uid!=='p1')}))
 const dataset=(h:PlacementHistory,offset:number)=>({spec:{start:new Date(start+offset-120000).toISOString(),workloadStartedAt:new Date(start+offset).toISOString(),end:new Date(start+offset+60000).toISOString(),placement:h}}) as TimeSeriesDataset
 const d=aggregateZonePlacement([dataset(a,0),dataset(b,3600000)])!
 const edge=d.rows.find(r=>r.label==='edge')!
 expect(d.cells[0].counts.get(edge.id)).toBe(1.5)
 expect(d.cells[1].observed).toBe(false)
 expect(d.cells[1].counts.size).toBe(0)
 expect(d.cells[2].observed).toBe(false)
 expect(d.cells[3].observed).toBe(true)
 expect(d.cells[3].counts.get(edge.id)).toBe(0)
 expect(d.end).toBe(60000)
})
it('aligns different resolutions on the coarsest grid and uses only the shared duration',()=>{
 const a=fixture(), b=fixture()
 b.stepSeconds=30
 b.snapshots=[{...b.snapshots[0]},{...b.snapshots[0],timestamp:new Date(start+30000).toISOString()}]
 const dataset=(h:PlacementHistory,duration:number)=>({spec:{start:new Date(start).toISOString(),end:new Date(start+duration).toISOString(),placement:h}}) as TimeSeriesDataset
 const d=aggregateZonePlacement([dataset(a,60000),dataset(b,30000)])!
 expect(d.cells.map(c=>c.time)).toEqual([0,30000])
 expect(d.end).toBe(30000)
 expect(aggregateZonePlacement([dataset(a,60000),{spec:{start:new Date(start).toISOString(),end:new Date(start+60000).toISOString()}} as TimeSeriesDataset])).toBeUndefined()
})
it('renders a heatmap with scheduled replica counts and standard export actions, omitting unavailable histories',()=>{
 const dataset={spec:{start:new Date(start-120000).toISOString(),workloadStartedAt:new Date(start).toISOString(),end:new Date(start+60000).toISOString(),placement:fixture()}} as TimeSeriesDataset
 const view=render(<PlacementHeatmap dataset={dataset}/>)
 expect(screen.getByRole('img',{name:'Application placement by zone heatmap'})).toBeInTheDocument()
 expect(screen.getByText('edge · 0.00 min · 2 scheduled replicas')).toBeInTheDocument()
 expect(screen.getByText('No samples')).toBeInTheDocument()
 expect(screen.getByRole('button',{name:'PDF'})).toBeInTheDocument()
 expect(screen.getByRole('button',{name:'PNG'})).toBeInTheDocument()
 const svg=view.container.querySelector('svg')!
 const pdf=new TextDecoder().decode(svgToPDF(svg))
 expect(pdf).toContain('(Scheduled replicas) Tj')
 expect(pdf).toContain('0.0314 0.1882 0.4196 rg')
 expect(pdf).toContain(' re S')
 expect(pdf).not.toContain(' re B')
 expect(svg.querySelector('rect[shape-rendering="crispEdges"]')).not.toBeNull()
 view.rerender(<PlacementHeatmap dataset={{...dataset,spec:{...dataset.spec,placement:undefined}}}/>)
 expect(screen.queryByRole('img')).not.toBeInTheDocument()
})
