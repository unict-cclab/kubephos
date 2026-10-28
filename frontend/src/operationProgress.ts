import type {Operation} from './types'

export function operationProgressNodes(operation: Operation) {
  return operation.plan.steps.map(planned => {
    // Persisted step IDs include the operation ID; older fixtures may use the plan ID.
    const step = operation.steps.find(item => item.id === `${operation.id}_${planned.id}`)
      ?? operation.steps.find(item => item.id === planned.id)
    return {id: planned.id, title: planned.name, status: step?.status ?? 'pending', detail: step?.error || undefined, operationId: operation.id}
  })
}
