import {comparisonTransferables, prepareComparison, type ComparisonJob} from './comparisonPreparation'

self.onmessage = async (event: MessageEvent<ComparisonJob>) => {
  try {
    const result = await prepareComparison(event.data, new AbortController().signal, message => self.postMessage({type: 'progress', message}), undefined, result => self.postMessage({type: 'statistics', result}))
    // Packed curves move to the page without a copy.
    self.postMessage({type: 'ready', result}, {transfer: comparisonTransferables(result)})
  } catch (cause) {
    self.postMessage({type: 'error', message: cause instanceof Error ? cause.message : 'Could not prepare the comparison.'})
  }
}
