import {describe, expect, it} from 'vitest'
import type {ManagedResource, PipelineRun} from './types'
import {retriesOnlyClusterMesh} from './clusterRetry'

describe('mesh-only cluster retry', () => {
 const resource = {spec:{mode:'multi'},pipelineRunId:'original',recreationPipelineRunId:'retry'} as unknown as ManagedResource
 const run = {id:'retry',status:'failed',stages:[{stageId:'c1-kubernetes',status:'succeeded',operationId:'existing-cluster'}, {stageId:'mesh',pluginId:'io.kubephos.cluster.cilium.mesh',status:'failed'}]} as PipelineRun
 it('keeps existing clusters when only the final mesh stage failed', () => expect(retriesOnlyClusterMesh(resource,[run])).toBe(true))
 it('requires all earlier stages to have succeeded', () => expect(retriesOnlyClusterMesh(resource,[{...run,stages:run.stages.map((stage,i)=>i===0?{...stage,status:'failed'}:stage)}])).toBe(false))
 it('does not resume a running pipeline', () => expect(retriesOnlyClusterMesh(resource,[{...run,status:'running'}])).toBe(false))
 it('does not use a previous run instead of the latest retry', () => expect(retriesOnlyClusterMesh(resource,[{...run,id:'original'}])).toBe(false))
})
