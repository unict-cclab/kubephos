# Experiments

## Configure and run

1. Add a Proxmox connection in **Infrastructure**.
2. Create a VM template, Harbor registry and NFS server.
3. Create a single Kubernetes cluster and wait until it is ready.
4. In **Configurations**, choose an application or external destination, then
   configure monitoring, load, network conditions and optional placement strategies.
5. Run the configuration, or combine two to eight compatible configurations in a suite.
6. Follow execution in **Experiments** and inspect **Statistics**, **Plots** and **Export**.

A suite saves configuration order, repetitions, plot labels and colors. Runs execute
sequentially. Starting an experiment freezes its complete configuration: later edits
do not change recorded settings or results. Single configurations referenced by a
suite cannot be deleted until that reference is removed.

**Namespace** is optional: leaving it empty keeps the generated namespace behavior.
A named application namespace is deleted and recreated before every run, then
removed during cleanup. System and platform namespaces are protected. Concurrent
runs cannot share a named namespace on the same cluster.

**Start mode** defaults to **Automatic**. Choose **Manual**
to deploy the application and configured components first. The run becomes
**Initialized** and waits while you prepare any external orchestration tooling.
Select **Start** in the experiment detail or use the play button in the list to begin the load and measurement
window. Each repetition waits separately, including runs inside a suite. You can
stop a waiting experiment to clean up its resources. Editing the configuration
changes future executions; existing experiments keep their saved settings.

**Application → Istio** enables injection by default for HTTP applications, with
per-service overrides. Native TCP applications retain their default opt-out.
Cilium multi cluster groups do not use Istio.

Monitoring follows Load profile. Network conditions can remain static
or use phases with individual latency, jitter, bandwidth, packet loss and directed
link overrides. Phase durations must sum to the load profile duration. A phase
without effects restores the unshaped channel. Network and load profiles share
the same start time; setup and readiness checks do not consume phase time.
New zone and cluster links are bidirectional by default; disable **Bidirectional**
to configure one direction. Existing links retain their saved direction. Latency
is added per direction, so a symmetric link with X ms adds approximately 2X ms
to the round-trip time.
Workload, traffic distribution and network phases can be cloned independently.
Sinusoidal workload phases use minimum and maximum requests per second, with
minimum >= 0 and maximum > minimum. Period and phase offset retain their meaning.
Existing baseline/amplitude profiles keep their original curve until their range
is edited.

Multi cluster provisioning uses Cilium ClusterMesh. Running experiments on a
multi cluster group awaits the multicluster deployment integration.

## Access and sharing

Configurations and experiments are private by default. Owners and administrators
can share read access with selected users or all signed-in users. Sharing an
experiment also grants access to its configuration and comparative source
configurations. Revocation removes inherited access unless another share grants it.

Sharing a configuration does not share its experiments or grant cluster access.
Recipients cannot edit, delete, execute or reshare another user's resources.
Sensitive artifacts are not available through ordinary result downloads.

## Applications and resources

CPU and memory requests/limits are optional across the catalog. Empty fields omit
these settings from application containers, init containers and auxiliary jobs.
Service overrides inherit the shared values. Kubernetes admission policies may
supply namespace defaults; a limit without a request becomes the request.

**Application → Placement** defines a shared node selector and tolerations.
Service overrides can inherit these values, replace them with custom values, or
select **None** to omit the shared custom settings for that service. Existing
KubePhos placement constraints remain in force; conflicting selectors are rejected.
These settings also apply to standalone application deployments.

Application pools support **Labels** and **Taints** in single and multi cluster
creation. Master and Management retain their managed settings. For single clusters,
**Manage nodes** can update application pool settings when no experiments are active.
A toleration permits placement on a tainted node; use a matching node selector when
placement on that pool is required. `NoExecute` taints can evict existing pods that
do not tolerate them.

Catalog deployment manifests and Locust scripts are examples. Experiment manifests
and load scripts are generated from the saved configuration.

### LocalAI

Add model services and choose GGUF models from **Catalog → AI models**. Service order
defines aliases `m1`, `m2`, and so on; a model can be used by multiple services.
Configure replicas, prompt and output-token limit in application settings and
traffic in the load profile. Each replica defaults to one CPU thread.

Register a model using its download URL. Hugging Face metadata pins the file to a
repository commit; other hosts can defer checksums to the download Job. A run's
Job verifies the GGUF header, SHA-256 and size before inference starts. Models and
checksums are snapshotted, so catalog changes do not affect existing experiments.
The managed ReadWriteMany storage class supplies each run's model cache.

### Redis Cluster

Redis uses native TCP without Envoy sidecars. The Locust driver handles cluster
routing and MOVED/ASK redirects. It supports GET, SET, HSET, LPUSH, LPOP, LRANGE and
a GET/SET mix. Bootstrap initializes data and waits for replication before load.
LPOP can exhaust lists; LPUSH grows them while the workload runs.

Network injection remains available. Geographic HTTP proxy distribution does not
apply to native Redis traffic. Locust provides end-to-end measurements. The profile
uses duration/RPS controls; it does not reproduce the Diktyo paper's fixed-request
redis-benchmark execution.

### External destinations

Choose **External destination**, then supply the HTTP(S) URL, Locust image,
script and optional string parameters. Parameters are available in
`KUBEPHOS_PARAMETERS` and `KUBEPHOS_PARAM_<NAME>` environment variables.
KubePhos creates only the isolated load-driver namespace and leaves the remote
application unchanged. Per-service application metrics are unavailable without a
verified Kubernetes topology.

## Metrics and comparisons

| Measurement | Meaning |
| --- | --- |
| End-to-end throughput, failures and response-time P95 | Locust request/operation events, including timeouts and script result decisions |
| Response-time P95 | Histogram estimate over rolling one-minute windows, sampled at Data resolution |
| Per-service metrics | Istio telemetry; unavailable where sidecar telemetry is absent |
| Scheduling cycle duration | Native scheduler algorithm cycle, excluding binding and Pod startup |
| Descheduling cycle duration | Complete native descheduler loop |
| Descheduler evictions | Successful API evictions for the application's namespace |
| Pod scheduling latency | Pod creation to its first `PodScheduled=True` transition, including queueing and retries |

Scheduling and descheduling durations are event-weighted means: observed duration
sums divided by cycle counts, handling process resets. They appear as aggregates
in Statistics; temporal samples remain in the complete dataset export. Default
scheduler counters have cluster scope, while dedicated strategy counters have
experiment scope. Missing counters remain unavailable, not zero.

The scheduling observer is read-only and tracks application Pods by UID, including
replacements. Kubernetes condition timestamps have one-second resolution: zero
latency means scheduling completed within that same timestamp second. Short-lived
counter events or resets wholly between scrapes cannot be reconstructed.

**All runs** shows one mean curve per approach and a shaded band of one sample
standard deviation between runs. Windows align to workload start at Data resolution
(the coarsest grid when resolutions differ). Each run contributes equally; only
windows present in every included run are shown. Warmup is excluded, missing windows
are not interpolated, and a single run has no deviation band. Averaged window P95
values are not a pooled request percentile. Individual-run statistics remain available.
Suite labels and colors apply to aggregate plots and exports.

Existing datasets retain their original measurement definitions. New end-to-end
measurements record `endToEndSource: locust`. Cluster Lens uses the same Locust
counters and histogram for its application statistics.

## Cluster maintenance

Administrators can use **Manage nodes** to migrate VMs, scale application pools,
and add/remove application pools. Master and management pools and their node
counts remain fixed. Maintenance requires a ready cluster with no queued or
running experiments; experiment admission is blocked during maintenance.
Existing VMIDs, addresses and keys are preserved. New nodes require unused
VMID/IP allocations; removed nodes are drained before their VMs are deleted.

## Independent application deployments

In **Deployments**, select **Deploy application**, a ready cluster, a catalog
application and a namespace. The Application step uses the same parameters,
initial replicas, resources, topology and Istio settings as experiment
configurations. No load generator or experiment is started.

A multi cluster group receives a complete copy on every member, using the same
namespace. Cilium groups keep Istio disabled. Activity records each member's
installation and readiness checks. Deployments stay installed until removed;
removal deletes only the namespaces owned by their saved deployment lifecycle.
If installation fails on a member, Activity retains the details and removal can
clean up the members that were already deployed.

Namespaces must be new. A namespace used by an active experiment, including an
initialized run or a run undergoing cleanup, cannot be used for an independent
deployment. Conversely, experiments cannot use a namespace reserved by an
independent deployment until its cleanup succeeds. Owners and administrators
can view and remove deployments; users can target only their assigned clusters.
