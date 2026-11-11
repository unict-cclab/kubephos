import {expect, it, vi} from 'vitest'
import {prepareComparison, type ComparisonJob} from './comparisonPreparation'
import {aggregatePlotMetric, statisticsCSV, variantStatistics, type LoadedDataset} from './resultsData'
import type {Experiment, TimeSeriesDataset} from './types'

function source(id: string, values: number[]): LoadedDataset {
  return {artifact: {id, name:id, digest:id, type:'TimeSeriesDataset', version:'v1alpha1', sensitive:false} as LoadedDataset['artifact'], label:id, dataset:{apiVersion:'artifacts.kubephos.dev/v1alpha1',kind:'TimeSeriesDataset',metadata:{name:id,version:'v1alpha1'},spec:{applicationRef:'app',clusterServer:'https://cluster.example',source:{kind:'prometheus',version:'v1',profile:'application'},summary:{metrics:1,samples:values.length,series:1},namespace:'test',start:'2026-11-11T00:00:00Z',end:'2026-11-11T00:02:00Z',stepSeconds:60,series:[{metric:'cpu.cores',unit:'cores',aggregation:'sum',labels:{deployment:'api',pod:'raw-pod-label'},points:values.map((value,i)=>({timestamp:`2026-11-11T00:0${i}:00Z`,value}))}]}} as TimeSeriesDataset}
}
it('prepares the identical one-versus-three-run curves, statistics, colors and export outside the renderer', async () => {
  const datasets = [source('single',[10,20]), source('a',[2,4]), source('b',[4,6]), source('c',[6,8])]
  const experiment = {id:'comparison',kind:'comparison',variants:[{id:'one',name:'Same',alias:'Same',color:'#123456',trials:[{resultArtifactId:'single'}]},{id:'many',name:'Same',alias:'Same',color:'#abcdef',trials:datasets.slice(1).map(item=>({resultArtifactId:item.artifact.id}))}]} as Experiment
  const job: ComparisonJob = {artifacts:datasets.map(item=>item.artifact),experiment,configurations:{}}
  const progress=vi.fn(), partial=vi.fn()
  const result = await prepareComparison(job,new AbortController().signal,progress,async artifact=>structuredClone(datasets.find(item=>item.artifact.id===artifact.id)!.dataset),partial)
  const presented = datasets.map(item=>({...item,label:'Same'}))
  expect(result.plots['cpu.cores']).toEqual(aggregatePlotMetric(presented,'cpu.cores',experiment))
  expect(result.columns.map(column=>column.rows)).toEqual(experiment.variants.map(variant=>variantStatistics({...experiment,variants:[variant]},presented,['cpu.cores'])))
  expect(result.services['cpu.cores'].rows.map(row=>row.groupID)).toEqual(['one','many'])
  expect(result.csv).toBe(statisticsCSV(presented,['cpu.cores'],experiment))
  expect(result.complete).toBe(true)
  expect(partial).toHaveBeenCalledWith(expect.objectContaining({complete:false,plots:{},csv:''}))
  expect(progress.mock.calls.map(([stage])=>stage)).toEqual(['Loading results…','Preparing statistics…','Preparing plots…'])
  expect(JSON.stringify(result)).not.toContain('raw-pod-label')
  expect(result.columns.every(column=>column.variant.trials.length===0)).toBe(true)
})
it('stops preparation when the result view is abandoned', async () => {
  const item=source('one',[1,2])
  const controller=new AbortController()
  const job={artifacts:[item.artifact],experiment:{variants:[{id:'one',trials:[{resultArtifactId:'one'}]}]} as Experiment,configurations:{}}
  await expect(prepareComparison(job,controller.signal,()=>{},async()=>{controller.abort();return item.dataset})).rejects.toMatchObject({name:'AbortError'})
})
