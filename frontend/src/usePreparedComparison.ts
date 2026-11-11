import {useEffect, useState} from 'react'
import {prepareComparison, type ComparisonJob, type ComparisonProgress, type PreparedComparison} from './comparisonPreparation'

export function usePreparedComparison(job: ComparisonJob, revision = 0) {
  const [state, setState] = useState<{job: ComparisonJob; result?: PreparedComparison; progress?: ComparisonProgress; error?: string}>({job, progress: 'Loading results…'})
  useEffect(() => {
    const controller = new AbortController()
    setState({job, progress: 'Loading results…'})
    let worker: Worker | undefined
    const ready = (result: PreparedComparison) => {if (!controller.signal.aborted) setState({job, result})}
    const failure = (cause: unknown) => {if (!controller.signal.aborted) setState({job, error: cause instanceof Error ? cause.message : 'Could not prepare the comparison.'})}
    try {
      if (typeof Worker === 'undefined') {
        // Non-browser consumers (including unit tests) use the identical calculation.
        void prepareComparison(job, controller.signal, progress => {if (!controller.signal.aborted) setState(previous => ({...previous, job, progress}))}, undefined, result => {if (!controller.signal.aborted) setState({job, result})}).then(ready, failure)
      } else {
        worker = new Worker(new URL('./comparison.worker.ts', import.meta.url), {type: 'module'})
        worker.onmessage = ({data}) => {
          if (controller.signal.aborted) return
          if (data.type === 'ready') {ready(data.result); worker?.terminate()}
          else if (data.type === 'error') {failure(new Error(data.message)); worker?.terminate()}
          else if (data.type === 'statistics') setState({job, result: data.result})
          else if (data.type === 'progress') setState(previous => ({...previous, job, progress: data.message}))
        }
        worker.onerror = () => {failure(new Error('Could not prepare the comparison. Try again.')); worker?.terminate()}
        worker.postMessage(job)
      }
    } catch (cause) {failure(cause)}
    return () => {controller.abort(); worker?.terminate()}
  }, [job, revision])
  return state.job === job ? state : {job, progress: 'Loading results…' as ComparisonProgress}
}
