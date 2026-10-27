export interface ResourceAttributionFields {
  createdAt?: string
  updatedAt?: string
  createdBy?: string
  modifiedBy?: string
}

export type Role = 'admin' | 'user'
export type View = 'overview' | 'infrastructure' | 'kubernetes' | 'experiments' | 'results' | 'catalog' | 'users' | 'administration'

export interface User extends ResourceAttributionFields {
  id: string
  username: string
  email?: string
  role: Role
  enabled?: boolean
  createdAt?: string
  updatedAt?: string
}

export interface ClusterUserAssignment {
  userId: string
  clusterResourceId: string
  assignedBy: string
  createdAt: string
}

export interface Session {
  authenticated: boolean
  setupRequired?: boolean
  csrfToken?: string
  user?: User
  loginURL?: string
}

export interface SchemaProperty {
  type?: 'string' | 'integer' | 'number' | 'boolean' | 'object' | 'array'
  title?: string
  description?: string
  default?: unknown
  enum?: Array<string | number | boolean>
  items?: SchemaProperty
  minItems?: number
  maxItems?: number
  'x-kubephos-item-prefix'?: string
  format?: string
  minimum?: number
  maximum?: number
  multipleOf?: number
  minLength?: number
  maxLength?: number
  writeOnly?: boolean
  required?: string[]
  properties?: Record<string, SchemaProperty>
  additionalProperties?: boolean
  'x-kubephos-provider'?: string
  'x-kubephos-secret-kind'?: string
  'x-kubephos-required-trait'?: string
  'x-kubephos-artifact-type'?: string
  'x-kubephos-artifact-version'?: string
  'x-kubephos-primary-action'?: boolean
  'x-kubephos-multiline'?: boolean
  'x-kubephos-schema-from-application'?: string
  'x-kubephos-visible-when'?: {property: string; values: Array<string | number | boolean>}
  'x-kubephos-hidden'?: boolean
  'x-kubephos-advanced'?: boolean
  'x-kubephos-order'?: number
}

export interface JsonSchema {
  type?: string
  required?: string[]
  properties?: Record<string, SchemaProperty>
  'x-kubephos-dynamic-topology'?: boolean
  'x-kubephos-service-prefix'?: string
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

export interface PluginPackage extends ResourceAttributionFields {
	sequence: number
	pluginId: string
	version: string
	digest: string
	descriptorDigest: string
	active: boolean
	createdAt: string
	updatedAt: string
}

export interface PluginImportJob extends ResourceAttributionFields {
  id: string
  status: 'queued' | 'inspecting' | 'validating' | 'activating' | 'succeeded' | 'failed'
  progress: number
  message: string
  error?: string
  descriptorDigest: string
  manifest?: Plugin
  pluginId?: string
  version?: string
  digest?: string
  packageSequence?: number
  createdAt: string
  startedAt?: string
  completedAt?: string
  updatedAt: string
}

export interface CatalogModel extends ResourceAttributionFields {
  id: string
  url: string
  sha256: string
  size: number
  backend: string
  quantization: string
  origin: 'built-in' | 'custom'
  enabled: boolean
}

export interface CatalogStrategy extends ResourceAttributionFields {
  id: string
  workspaceId: string
  harborResourceId?: string
  name: string
  kind: 'scheduler' | 'descheduler' | 'autoscaler'
  sourceImage: string
  sourceDigest?: string
  mirroredImage?: string
  mirroredDigest?: string
  defaultConfiguration: Record<string, unknown>
  operationId?: string
  artifactId?: string
  status: string
  error?: string
  createdAt: string
  updatedAt: string
}

export interface PluginRuntimeRegistry {
  endpointArtifactId: string
  credentialArtifactId: string
}

export interface PluginRuntimeProfile {
  endpointArtifactId: string
  credentialArtifactId: string
  registries: PluginRuntimeRegistry[]
  createdAt?: string
  updatedAt?: string
}

export interface PluginRuntimeStatus {
  configured: boolean
  status: string
  message: string
  profile?: PluginRuntimeProfile
}

export interface CredentialDefinition {
  kind: string
  name: string
  description: string
  schema: JsonSchema
}

export interface Workspace extends ResourceAttributionFields {
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

export interface ValidationReport {
  valid: boolean
  issues: ValidationIssue[]
  checkedAt?: string
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
  aggregation?: 'sum' | 'mean'
  labels: Record<string, string>
  points: MetricPoint[]
}

export interface ControlMetricSummary {
  timestampResolutionSeconds?: number
  metric: string
  status: 'available' | 'no_events' | 'unavailable' | 'not_configured'
  scope: 'cluster' | 'experiment'
  source?: string
  count?: number
  mean?: number
  total?: number
  reason?: string
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
    endToEndSource?: 'locust'
    workloadStartedAt?: string
    controlMetrics?: ControlMetricSummary[]
    sloMilliseconds?: number
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
  startedAt?: string
  completedAt?: string
}

export interface ExperimentVariant {
  id: string
  experimentId: string
  position: number
  name: string
  alias?: string
  color?: string
  pipelineId?: string
  pipelineHash?: string
  configurationId?: string
  configuration: Record<string, unknown>
  trials: ExperimentTrial[]
}

export interface Experiment extends ResourceAttributionFields {
  configurationSnapshot?: Record<string, unknown>
  id: string
  workspaceId: string
  configurationId?: string
  ownerUserId?: string
  ownerUsername?: string
  kind: 'comparison' | 'instance' | 'suite'
  name: string
  description: string
  cancelRequested?: boolean
  status: string
  resultType: string
  resultVersion: string
  variants: ExperimentVariant[]
  scheduledFor?: string
  createdAt: string
  updatedAt: string
}

export interface ExperimentFigure {
  id: string
  experimentId: string
  metric: string
  format: 'png' | 'pdf'
  sourceArtifactIds: string[]
  digest: string
  sizeBytes: number
  createdAt: string
}

export interface ExperimentConfigurationComponent {
  id: string
  strategyId?: string
  pluginId: string
  pluginVersion?: string
  pluginDigest?: string
  capability: string
  configuration: Record<string, unknown>
  targets: {include: string[]; exclude: string[]}
}

export interface ExperimentConfiguration extends ResourceAttributionFields {
  kind?: 'single' | 'suite'
  id: string
  workspaceId: string
  clusterResourceId: string
  ownerUserId?: string
  ownerUsername?: string
  name: string
  description: string
  applicationRef: string
  applicationDigest: string
	definition: {
    kind?: 'single' | 'suite'
    variants?: Array<{configurationId: string; alias: string; color: string}>
    runs?: number
	applicationValues: Record<string, unknown>
		destinationMode?: 'application' | 'external'
		externalDestination?: {targetUrl: string; runtimeImage: string; script: string; parameters?: Record<string, string>}
		podDisruptionBudgetMinAvailable?: number
		components: ExperimentConfigurationComponent[]
	}
  validation: ValidationReport
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

export interface Pipeline extends ResourceAttributionFields {
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
  cleanupOperationId?: string
  cleanupStatus?: string
  cleanupError?: string
}

export interface PipelineRun extends ResourceAttributionFields {
  id: string
  pipelineId: string
  workspaceId: string
  clusterResourceId?: string
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

export interface Operation extends ResourceAttributionFields {
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
  workload?: {apiVersion?: string; kind?: string; name?: string}
  index?: number
  dependencies?: string[]
}

export interface ApplicationEndpoint {
  id?: string
  component?: string
  service?: string
  port?: number
  protocol?: string
  path?: string
}

export interface ApplicationLoadScenario {
  id?: string
  engine?: string
  script?: string
  content?: string
  runtimeImage?: string
  targetEndpoint?: string
}

export interface Application extends ResourceAttributionFields {
  id: string
  reference: string
  name: string
  description: string
  origin: string
  digest: string
  descriptor: {
    spec?: {
      package?: {type?: string; format?: string; repository?: string; revision?: string; path?: string; entrypoint?: string; additionalEntrypoints?: string[]}
      materializer?: string
      materializerConfig?: Record<string, unknown>
      interface?: {group?: string; components?: ApplicationComponent[]; endpoints?: ApplicationEndpoint[]; loadScenarios?: ApplicationLoadScenario[]}
      valuesSchema?: JsonSchema
      defaults?: Record<string, unknown>
    }
  }
  createdAt?: string
  updatedAt?: string
}

export interface Credential extends ResourceAttributionFields {
  id: string
  name: string
  kind: string
  fingerprint: string
}

export interface Connection extends ResourceAttributionFields {
  id: string
  name: string
  description?: string
  provider: string
  pluginId: string
  createdAt: string
  updatedAt: string
}

export interface ManagedResource extends ResourceAttributionFields {
  id: string
  workspaceId: string
  name: string
  kind: string
  provider: string
  connectionId?: string
  operationId?: string
  pipelineId?: string
  pipelineRunId?: string
  artifactId?: string
  deletionOperationId?: string
  deletionPipelineId?: string
  deletionPipelineRunId?: string
  recreationPipelineId?: string
  recreationPipelineRunId?: string
  status: string
  error?: string
  validation: ValidationReport
  spec: Record<string, unknown>
  createdAt: string
  updatedAt: string
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

export interface PipelineRunLogEntry extends LogEntry {
  stageId: string
  stageTitle: string
  stagePosition: number
  phase: 'execution' | 'cleanup'
}

export interface PipelineStrategyLog {
  kind: 'scheduler' | 'descheduler' | 'autoscaler'
  title: string
  namespace?: string
  resource?: string
  state: 'waiting' | 'available' | 'unavailable'
  lines: string[]
  message?: string
}

export interface SystemStatus {
  version: string
  status: string
  features?: {ociPluginImport?: boolean}
  pluginRuntime?: {status: string; message: string}
  stats: {workspaces: number; activeOperations: number; readyOperations: number; failedOperations: number}
}

export interface PlatformData {
  system: SystemStatus | null
	pluginRuntime: PluginRuntimeStatus | null
  workspaces: Workspace[]
  experiments: Experiment[]
  experimentConfigurations: ExperimentConfiguration[]
  pipelines: Pipeline[]
  pipelineRuns: PipelineRun[]
  operations: Operation[]
  artifacts: Artifact[]
  plugins: Plugin[]
  pluginPackages: PluginPackage[]
  pluginImports: PluginImportJob[]
  applications: Application[]
  strategies: CatalogStrategy[]
  credentials: Credential[]
  connections: Connection[]
  machineTemplates: ManagedResource[]
  infrastructureServices: ManagedResource[]
  kubernetesClusters: ManagedResource[]
  resources: InfrastructureResource[]
  audit: AuditEvent[]
  users: User[]
  clusterAssignments: ClusterUserAssignment[]
}
