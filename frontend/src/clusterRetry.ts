import type {ManagedResource, PipelineRun} from './types'

export function retriesOnlyClusterMesh(resource: ManagedResource | undefined, runs: PipelineRun[]): boolean {
  if (resource?.spec.mode !== 'multi') return false
  const run = runs.find(run => run.id === (resource.recreationPipelineRunId || resource.pipelineRunId))
  if (!run || !['failed', 'canceled'].includes(run.status) || run.stages.length < 2) return false
  const last = run.stages.at(-1)!
  return last.stageId === 'mesh' && last.pluginId === 'io.kubephos.cluster.cilium.mesh' && ['failed', 'canceled'].includes(last.status)
    && run.stages.slice(0, -1).every(stage => stage.status === 'succeeded' && Boolean(stage.operationId))
}
