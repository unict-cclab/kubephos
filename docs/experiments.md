# Experiments

## First experiment

From the web interface:

1. Open **Infrastructure** and add a Proxmox connection.
2. Create a VM template, Harbor registry, and NFS server.
3. Open **Kubernetes** and create a cluster.
4. Wait until every cluster component is healthy.
5. Open **Configurations** and create an experiment configuration.
6. Select the cluster, application, load profile, and optional strategies.
7. Run the configuration and choose the number of sequential runs.
8. Open **Experiments** to follow progress and inspect results.

### Privacy and sharing

Configurations and experiments are private by default, including existing records when upgrading. Owners and administrators can use **Share** to grant read access to selected users or all signed-in users. Sharing an experiment also grants access to its configuration and the source configurations of comparative runs. Revoking an experiment share removes that inherited access unless another share still grants it. Sharing a configuration does not share its experiments or grant access to the cluster. Recipients cannot edit, delete, run or reshare someone else's resources. Sensitive artifacts remain unavailable for browser download.

### LocalAI inference experiments

Select **LocalAI** from the application catalog. Add a service and choose its model from the list; the order defines the service aliases `m1`, `m2`, and so on. A model can be selected for multiple services. The CPU model list contains nine official Qwen GGUF models: Qwen2.5 Instruct and Coder at 0.5B, 1.5B and 3B (Q4_K_M), and Qwen3 at 0.6B, 1.7B (Q8_0) and 4B (Q4_K_M). The three smaller Qwen2.5 services remain the default selection.

Open **Catalog → AI models** to register a custom GGUF using only its download URL. Hugging Face file metadata is resolved and the URL is pinned to a repository commit; other hosts can defer checksum calculation to the experiment download Job. The Job validates the GGUF file header and records its SHA-256 and byte count in the shared cache before allowing inference to start. Models can be edited, archived and enabled again. The LocalAI model selector updates immediately after saving; no image build or redeployment is needed. The initial nine models are seeded once and edits survive platform restarts. Model URLs and checksums are snapshotted into each generated manifest so catalog changes do not alter existing runs.

Set replicas globally or override them per model service. CPU and memory requests and limits are independent optional fields. Leaving them blank omits the corresponding fields from application containers, including package CPU and memory defaults; Kubernetes admission policies may still supply defaults. A limit without a request causes Kubernetes to copy that limit into the request. CPU and memory defaults are also removed from init containers and auxiliary application jobs. Other resource types are preserved. Catalog defaults, Redis bootstrap and Locust runtimes do not supply CPU or memory requests or limits. The shared service resource editor groups CPU and memory requests and limits and supports partial overrides per service. Blank override fields inherit the defaults; overrides are checked against the effective limits. All built-in applications expose the same settings; node proxies have no fixed CPU or memory requests or limits.

The application uses the managed cluster’s `shared-storage` ReadWriteMany storage class automatically. A Job downloads and verifies models and installs the shared CPU backend before inference starts. Each run gets its own model cache PVC in its isolated namespace. The generator creates Nginx upstreams and `X-Model` routing directly in each node proxy from the selected services; there is no separate inference router. Service names resolve through the pod’s standard Kubernetes DNS configuration, so no DNS IP or cluster domain is requested from the user. Node proxies retain the standard node and zone headers.

The generated Locust scenario sends non-streaming chat requests across the configured model services, using matching header and body aliases. Configure the prompt and maximum output tokens in application settings and the traffic pattern in the load profile. Each service replica defaults to one CPU thread.

### External destination experiment

In **Configurations**, select **External destination** instead of a catalog application. Provide the HTTP or HTTPS base URL, the Locust runtime image, a Locust file template, optional string parameters, and the temporal workload pattern. Each parameter is exposed to the Locust process both in the `KUBEPHOS_PARAMETERS` JSON object and as a deterministic `KUBEPHOS_PARAM_<NAME>` environment variable.

Each run creates an isolated, service-mesh-enabled namespace containing only the load driver. KubePhos does not deploy or modify the external application. End-to-end results use Locust measurements; per-service application metrics are omitted because the external destination does not expose a verified Kubernetes workload topology.


## Configurations and experiments

Configurations are reusable definitions. A **single configuration** defines an application or external destination, load and strategies. A **comparative configuration (suite)** selects two to eight compatible single configurations and saves their execution order, plot aliases, colors and repetitions.

An experiment executes either configuration type. Starting one freezes the suite recipe and the complete single-configuration settings; later edits do not change its results. Existing result plots and per-run diagnostics remain available, with the configuration snapshot downloadable from Export.

Migration `040-comparative-configurations` turns historical suites into reusable comparative configurations and preserves their experiments and recorded settings. A historical suite with deleted source configurations remains visible and must be edited before running again. Single configurations used by a suite cannot be deleted until that reference is removed.


## Cluster maintenance

Cluster administrators can use **Manage nodes** in the cluster detail to live migrate VMs between Proxmox servers, scale application pools, or add and remove application pools. Control plane and management pools and node counts remain fixed. Maintenance requires a ready cluster without queued or running experiments; database admission checks also prevent concurrent experiment starts. Existing VMIDs, addresses and access keys are preserved. New nodes require an explicitly selected unused VMID/address allocation. Removed application nodes are drained before uninstalling K3s and deleting their VMs. Local persistent volumes and disruption budgets can block removal; failed maintenance leaves the cluster unavailable for new experiments until recovery. Live migration requires the token's `VM.Migrate` privilege and compatible destination networking and storage.

### Redis Cluster benchmark

The Redis Cluster catalog application generates one persistent StatefulSet per primary or replica, with explicit primary–replica dependencies. Defaults are five primaries and one replica per primary. Shards, replicas, storage, persistence, CPU/memory resources, operation, read percentage, key count, payload size, initial list length and request timeout are configurable in application settings. Concurrent users, spawn rate, RPS and duration use the experiment load profile.

The Locust scenario uses native Redis TCP connections, cluster slot routing and MOVED/ASK redirects. Supported operations are SET, GET, HSET, LPUSH, LPOP and LRANGE over 100 or 500 elements, plus a configurable GET/SET mix. Bootstrap initializes the selected data type and waits for replication before load starts. LPOP can exhaust the initial lists; LPUSH grows them for the duration of the workload. Redis nodes and the load driver run without Envoy sidecars; inter-zone network injection remains available. Geographic HTTP proxy traffic is unavailable for native Redis.

Load throughput, failures and P95 come from Locust operation events and are scraped directly by Prometheus; operation-level plots are separate from HTTP service metrics. The Diktyo paper uses redis-benchmark with 50 clients and 250,000 requests; this duration/RPS-based Locust profile does not reproduce its fixed-request execution semantics.

### Scheduling and descheduling metrics

New experiment deployments record their placement start time. Metrics cover placement through the end of the load phase; workload plots retain their traffic-phase time origin. Managed schedulers and deschedulers install an experiment-scoped PodMonitor with a monitoring-only service-account identity. K3s default scheduler counters are read from the existing API-server scrape to avoid collecting duplicate copies from embedded component endpoints.

Scheduling duration measures the scheduler algorithm cycle; binding and pod startup are excluded. Descheduling duration measures the full native descheduler loop. Temporal points are the mean duration of cycles completed between counter observations at the configured data resolution. Run summaries divide observed duration sums by observed cycle counts, handling process resets before combining sources. These are event-weighted means, rather than means of interval means.

Evictions count successful descheduler API evictions for the application namespace; failed attempts and background requests are excluded. Statistics and CSV contain the run total. Scheduling and descheduling metrics appear as aggregates in Statistics; their temporal series remain available in the complete dataset export. A missing counter stays unavailable rather than becoming zero. Idle intervals have no duration sample. Legacy and current upstream descheduler metric names are supported without double-counting versions that publish both. Short-lived events or counter resets occurring entirely between scrapes cannot be reconstructed. Default scheduler metrics have cluster scope; dedicated strategy metrics have experiment scope. Existing result artifacts remain unchanged.

End-to-end throughput, failures and response-time P95 use Locust request events for HTTP and native protocols. Success and failure follow the Locust result, including timeouts and script `catch_response` decisions. P95 is estimated from the client-duration histogram over rolling one-minute windows, sampled at Data resolution. Istio remains the source for per-service metrics. Cluster Lens uses the same Locust counters and histogram for its application live statistics, with unavailable values shown as n/a. Existing datasets retain their original measurements; new datasets record `endToEndSource: locust`.

Pod scheduling latency is measured from each application Pod's creation timestamp to its first `PodScheduled=True` transition. A read-only observer in the isolated run namespace starts before application workloads, filters their selectors, and retains completed observations by UID after deletion or replacement. Prometheus scrapes these observations; each UID contributes once to the run statistics (including standard deviation, minimum, median and maximum when observations are complete), and temporal points group pods by scheduling completion at Data resolution. Kubernetes condition timestamps have one-second resolution, recorded in the dataset; a zero value means scheduling completed within the same timestamp second. This experiment-scoped latency includes queueing and retries for both default and dedicated schedulers. The native scheduling-cycle metric remains separate diagnostic evidence with its original scope.
