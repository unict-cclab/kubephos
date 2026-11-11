import {act, renderHook} from '@testing-library/react'
import {afterEach, expect, it, vi} from 'vitest'
import {usePreparedComparison} from './usePreparedComparison'
import type {ComparisonJob, PreparedComparison} from './comparisonPreparation'
import type {Experiment} from './types'

class TestWorker {
  static instances: TestWorker[] = []
  onmessage: ((event: MessageEvent) => void) | null = null
  onerror: (()=>void) | null = null
  postMessage = vi.fn()
  terminate = vi.fn()
  constructor() {TestWorker.instances.push(this)}
  emit(data: unknown) {this.onmessage?.({data} as MessageEvent)}
}
afterEach(()=>{vi.unstubAllGlobals();TestWorker.instances=[]})
const job={artifacts:[],experiment:{id:'first'} as Experiment,configurations:{}} as ComparisonJob
it('uses a worker, keeps partial statistics visible and ignores stale work after navigation',()=>{
  vi.stubGlobal('Worker',TestWorker)
  const view=renderHook(({job})=>usePreparedComparison(job),{initialProps:{job}})
  const first=TestWorker.instances[0]
  expect(first.postMessage).toHaveBeenCalledWith(job)
  const partial={complete:false,columns:[]} as unknown as PreparedComparison
  act(()=>first.emit({type:'statistics',result:partial}))
  act(()=>first.emit({type:'progress',message:'Preparing plots…'}))
  expect(view.result.current.result).toBe(partial)
  view.rerender({job})
  expect(TestWorker.instances).toHaveLength(1)
  const secondJob={...job,experiment:{...job.experiment,id:'second'}}
  view.rerender({job:secondJob})
  expect(first.terminate).toHaveBeenCalled()
  act(()=>first.emit({type:'ready',result:{complete:true}}))
  expect(view.result.current.result).toBeUndefined()
  const second=TestWorker.instances[1]
  const ready={complete:true} as PreparedComparison
  act(()=>second.emit({type:'ready',result:ready}))
  expect(view.result.current.result).toBe(ready)
  expect(second.terminate).toHaveBeenCalled()
  view.unmount()
  expect(second.terminate).toHaveBeenCalledTimes(2)
})
it('reports worker failures and starts fresh work when retried',()=>{
  vi.stubGlobal('Worker',TestWorker)
  const view=renderHook(({revision})=>usePreparedComparison(job,revision),{initialProps:{revision:0}})
  act(()=>TestWorker.instances[0].emit({type:'error',message:'Could not load results.'}))
  expect(view.result.current.error).toBe('Could not load results.')
  view.rerender({revision:1})
  expect(TestWorker.instances).toHaveLength(2)
  expect(view.result.current.error).toBeUndefined()
  act(()=>TestWorker.instances[1].onerror?.())
  expect(view.result.current.error).toContain('Try again')
})
