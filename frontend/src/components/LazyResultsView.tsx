import {lazy, Suspense} from 'react'
import type {ResultsViewProps} from './ResultsView'

const Results = lazy(() => import('./ResultsView').then(module => ({default: module.ResultsView})))

export function ResultsView(props: ResultsViewProps) {
  return <Suspense fallback={<div className="page-loading" role="status"><span className="loading-indicator" aria-hidden="true" /><strong>Preparing your results…</strong></div>}><Results {...props} /></Suspense>
}
