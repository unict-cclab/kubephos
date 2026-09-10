export type Role = 'admin' | 'operator' | 'viewer'
export type View = 'overview' | 'workspaces' | 'pipelines' | 'operations' | 'results' | 'catalog' | 'plugins' | 'infrastructure'

export interface User {
  id: string
  username: string
  role: Role
}

export interface Session {
  authenticated: boolean
  setupRequired?: boolean
  csrfToken?: string
  user?: User
}

export interface SchemaProperty {
  type?: 'string' | 'integer' | 'number' | 'boolean' | 'object' | 'array'
  title?: string
  description?: string
  default?: unknown
  enum?: Array<string | number>
  format?: string
  minimum?: number
  maximum?: number
  minLength?: number
  maxLength?: number
  writeOnly?: boolean
  'x-kubephos-provider'?: string
  'x-kubephos-secret-kind'?: string
  'x-kubephos-required-trait'?: string
  'x-kubephos-artifact-type'?: string
  'x-kubephos-artifact-version'?: string
}

export interface JsonSchema {
  type?: string
  required?: string[]
  properties?: Record<string, SchemaProperty>
}

export interface Plugin {
  id: string
  provider?: string
  name: string
  version: string
  description: string
  schema: JsonSchema
  capabilities?: string[]
  permissions?: string[]
  artifactInputs?: Array<{type: string; version: string}>
  artifactOutputs?: Array<{type: string; version: string}>
  credentialSchemas?: CredentialDefinition[]
  runtime: {kind: string; reference?: string; digest?: string}
}

export interface CredentialDefinition {
  kind: string
  name: string
  description: string
  schema: JsonSchema
}

export interface Workspace {
  id: string
  name: string
  description: string
  status: string
  createdAt: string
}

export interface ValidationIssue {
  level: string
  path?: string
  message: string
}

export interface HealthReport {
  status: string
  summary: string
  checks: Record<string, string>
}

export interface ResourceEffect {
  action: string
  kind: string
  name: string
  externalId: string
}

export interface PlanStep {
  id: string
  name: string
  mutating?: boolean
  cleanup?: boolean
  effects?: ResourceEffect[]
  artifactInputs?: Array<{name: string; type: string; version: string; artifactId?: string; fromStep?: string; fromOutput?: string}>
  outputs?: Array<{name: string; type: string; version: string; mediaType: string; source: string; sensitive?: boolean}>
}

export interface OperationStep {
  id: string
  position: number
  name: string
  status: string
  result?: unknown
  health?: HealthReport
  error?: string
}

export interface Artifact {
  id: string
  operationId: string
  workspaceId?: string
  stepId?: string
  outputName?: string
  name: string
  type: string
  version: string
  mediaType: string
  digest: string
  sizeBytes: number
  sensitive: boolean
  verifiedAt: string
}

export interface MetricPoint {
  timestamp: string
  value: number
}

export interface MetricSeries {
  metric: string
  unit: string
  labels: Record<string, string>
  points: MetricPoint[]
}

export interface TimeSeriesDataset {
  apiVersion: string
  kind: 'TimeSeriesDataset'
  metadata: {name: string; version: string}
  spec: {
    applicationRef: string
    clusterServer: string
    namespace: string
    start: string
    end: string
    stepSeconds: number
    source: {kind: string; version: string; profile: string}
    series: MetricSeries[]
    summary: {metrics: number; series: number; samples: number}
  }
}

export interface ExperimentTrial {
  id: string
  variantId: string
  position: number
  status: string
  operationId: string
  pipelineRunId?: string
  resultArtifactId: string
  error?: string
  createdAt: string
  completedAt?: string
}

export interface ExperimentVariant {
  id: string
  experimentId: string
  position: number
  name: string
  pipelineId?: string
  pipelineHash?: string
  configuration: Record<string, unknown>
  trials: ExperimentTrial[]
}

export interface Experiment {
  id: string
  workspaceId: string
  name: string
  description: string
  status: string
  resultType: string
  resultVersion: string
  variants: ExperimentVariant[]
  scheduledFor?: string
  createdAt: string
  updatedAt: string
}

export interface PipelineBinding {
  path: string
  fromStage: string
  fromOutput?: string
}

export interface PipelineStage {
  id: string
  pluginId: string
  title: string
  spec: Record<string, unknown>
  bindings?: PipelineBinding[]
}

export interface Pipeline {
  id: string
  workspaceId: string
  name: string
  description: string
  definition: {
    stages: PipelineStage[]
    result: {stage: string; output?: string; type?: string; version?: string}
  }
  resolution: {
    stages: Array<{id: string; pluginId: string; pluginVersion: string; pluginDigest?: string}>
    result: {type: string; version: string}
  }
  validation: {valid: boolean; issues: ValidationIssue[]}
  hash: string
  createdAt: string
  updatedAt: string
}

export interface PipelineRunStage {
  id: string
  runId: string
  position: number
  stageId: string
  pluginId: string
  title: string
  status: string
  operationId?: string
  error?: string
}

export interface PipelineRun {
  id: string
  pipelineId: string
  workspaceId: string
  name: string
  status: string
  pipelineHash: string
  resultType: string
  resultVersion: string
  resultArtifactId?: string
  cancelRequested: boolean
  error?: string
  createdAt: string
  queuedAt?: string
  scheduledFor?: string
  startedAt?: string
  completedAt?: string
  stages: PipelineRunStage[]
}

export interface Operation {
  id: string
  workspaceId: string
  pluginId: string
  pluginVersion: string
  pluginDigest?: string
  title: string
  status: string
  error?: string
  planHash: string
  plan: {steps: PlanStep[]}
  validation: {valid: boolean; issues: ValidationIssue[]}
  steps: OperationStep[]
  artifacts?: Artifact[]
  createdAt: string
}

export interface ApplicationComponent {
  id: string
  traits?: string[]
}

export interface Application {
  id: string
  reference: string
  name: string
  version: string
  description: string
  origin: string
  digest: string
  descriptor: {
    spec?: {
      package?: {type?: string; format?: string}
      interface?: {components?: ApplicationComponent[]; endpoints?: unknown[]; loadDrivers?: unknown[]}
    }
  }
}

export interface Credential {
  id: string
  name: string
  kind: string
  fingerprint: string
}

export interface Connection {
  id: string
  name: string
  provider: string
  pluginId: string
}

export interface InfrastructureResource {
  id: string
  provider: string
  externalId: string
  kind: string
  name: string
  state: string
  ownership: string
  protection: string
}

export interface AuditEvent {
  sequence: number
  actor: string
  action: string
  targetType: string
  targetId?: string
  outcome: string
  createdAt: string
}

export interface LogEntry {
  sequence: number
  level: string
  source: string
  message: string
  createdAt: string
}

export interface SystemStatus {
  version: string
  stats: {workspaces: number; activeOperations: number; readyOperations: number; failedOperations: number}
}

export interface PlatformData {
  system: SystemStatus | null
  workspaces: Workspace[]
  experiments: Experiment[]
  pipelines: Pipeline[]
  pipelineRuns: PipelineRun[]
  operations: Operation[]
  artifacts: Artifact[]
  plugins: Plugin[]
  applications: Application[]
  credentials: Credential[]
  connections: Connection[]
  resources: InfrastructureResource[]
  audit: AuditEvent[]
}
