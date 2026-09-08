# KubePhos — Piano di lavoro

> A modular Kubernetes workspace.

## Obiettivo

KubePhos sarà un motore generico e una piattaforma web per:

- creare infrastrutture Kubernetes;
- eseguire pipeline sperimentali composte da plug-in;
- confrontare varianti della stessa pipeline;
- eseguire esperimenti in background o pianificati;
- raccogliere, visualizzare ed esportare i risultati;
- conservare lo storico degli esperimenti.

## Architettura

```text
Browser
   |
   v
KubePhos app
API Go + frontend incorporato
   |
   +-- PostgreSQL
   |     configurazioni, storico, job e scheduling
   |
   +-- SeaweedFS
         log, metriche, dataset, plot ed export

KubePhos worker
stessa immagine dell'app, comando differente
   |
   +-- runtime dei plug-in
         |
         +-- sistemi esterni
         +-- infrastrutture
         +-- cluster
```

Il deployment userà Docker Compose con quattro servizi:

- `app`: API e frontend nello stesso container;
- `worker`: stessa immagine, avviata in modalità worker;
- `postgres`;
- `seaweedfs`.

```bash
docker compose up -d
docker compose up -d --scale worker=4
```

## Modello del dominio

- **Environment**: contesto persistente condiviso dagli step.
- **Experiment**: confronto completo e immutabile.
- **Variant**: strategia da valutare.
- **Trial**: singola ripetizione di una variante.
- **Step**: invocazione di un plug-in all'interno di un DAG.
- **Artifact**: log, metriche, configurazioni, plot o file esportabile.
- **Plugin**: estensione versionata che implementa il protocollo KubePhos.

Esempio:

```text
Experiment
  pipeline
    step A -> step B -> step C -> step D

Variant A:
  parametri X

Variant B:
  parametri Y

Trials per variante: 3
```

## Configurazione

Il percorso principale sarà una procedura guidata nella UI:

1. selezione di un template di pipeline;
2. configurazione dei plug-in che compongono gli step;
3. definizione delle varianti;
4. scelta del numero di ripetizioni;
5. esecuzione immediata o pianificata.

KubePhos genererà e conserverà una configurazione risolta contenente versioni e digest degli artefatti. YAML e JSON saranno disponibili soltanto come modalità avanzata.

Le credenziali saranno gestite separatamente e non compariranno nelle configurazioni degli esperimenti.

Il core non conterrà campi o logica dedicati a provider, distribuzioni Kubernetes, applicazioni, strategie, generatori di carico, collector, analyzer o formati di export. I form della UI saranno generati dagli schema pubblicati dai plug-in installati.

## Validazione preventiva

La validazione sarà obbligatoria e non produrrà modifiche sui sistemi esterni. Un esperimento potrà essere accodato soltanto dopo la validazione completa dell'intera pipeline.

Il processo comprenderà:

1. risoluzione delle versioni e dei digest di tutti i plug-in;
2. validazione delle configurazioni tramite JSON Schema;
3. verifica delle firme e delle policy di esecuzione;
4. costruzione del DAG e rilevamento di cicli o step irraggiungibili;
5. controllo dei tipi degli artifact tra produttori e consumatori;
6. verifica di dipendenze, capacità e compatibilità tra plug-in;
7. verifica della presenza dei secret richiesti senza esporne il contenuto;
8. esecuzione di `validate` e `plan` per ogni step;
9. preflight non mutante verso i sistemi esterni;
10. calcolo delle risorse richieste, dei timeout e del parallelismo possibile;
11. produzione del piano risolto e del report di validazione.

Il report distinguerà:

- **errori**, che impediscono l'esecuzione;
- **warning**, che richiedono conferma esplicita;
- **informazioni**, che descrivono il piano risultante.

Il piano validato sarà immutabile e identificato da un hash. L'esecuzione userà esattamente versioni, configurazioni e digest validati. Prima di avviare il primo step, il worker ripeterà i controlli dinamici soggetti a cambiamenti, senza ricostruire il piano.

```text
DRAFT -> VALIDATING -> READY -> QUEUED -> RUNNING
                 |
                 +-------> INVALID
```

Non sarà possibile passare direttamente da `DRAFT` a `QUEUED` o `RUNNING`.

## Health gate tra gli step

Il completamento del comando di uno step non sarà considerato una prova di successo. Dopo ogni esecuzione, KubePhos dovrà verificare le post-condizioni dichiarate dal plug-in e la salute delle risorse da cui dipende lo step successivo.

```text
PENDING
   |
   v
PRECHECKING
   |
   v
RUNNING
   |
   v
VERIFYING -------> FAILED
   |
   v
SUCCEEDED
```

Uno step potrà entrare in `SUCCEEDED` soltanto quando:

- il plug-in ha concluso l'operazione senza errori;
- tutti gli artifact attesi sono presenti e validi;
- le post-condizioni sono soddisfatte;
- le risorse create risultano `Healthy`;
- le dipendenze richieste dallo step successivo sono ancora `Healthy`;
- le condizioni rimangono stabili per la finestra configurata.

Prima di ogni step successivo, il worker eseguirà nuovamente i check dinamici sugli input e sulle dipendenze. Questo permette di rilevare errori e drift verificatisi dopo la validazione iniziale.

Gli stati di salute standard saranno:

- `Unknown`;
- `Progressing`;
- `Healthy`;
- `Degraded`;
- `Unhealthy`.

Il plug-in responsabile del cluster dovrà, per esempio, poter verificare API server, nodi, rete, DNS, storage e componenti di base. Gli altri plug-in dichiareranno i propri check di readiness e le dipendenze necessarie. Il core si limiterà a eseguire e imporre i report di salute senza conoscere i dettagli delle tecnologie.

Se la verifica fallisce, la policy dello step potrà:

- ripetere il check;
- rieseguire lo step se è idempotente;
- sospendere il run per consentire il debugging;
- eseguire una compensazione;
- interrompere il run;
- raccogliere un bundle diagnostico prima del cleanup.

Il comportamento predefinito sarà sospendere il run e conservare ambiente, log ed eventi per l'ispezione. Il cleanup automatico non dovrà eliminare le prove necessarie al debugging.

Durante workload ed esperimenti, i check critici continueranno in background. Il degrado di cluster o componenti verrà registrato e, in base alla policy, potrà mettere in pausa o interrompere il run.

## Concorrenza

Il backend Go userà goroutine e worker pool configurabili.

- Esperimenti indipendenti potranno essere eseguiti in parallelo.
- I trial potranno essere distribuiti tra più worker.
- Le installazioni indipendenti potranno essere eseguite parallelamente.
- Metriche e artefatti potranno essere raccolti concorrentemente.
- PostgreSQL gestirà lock e lease sui cluster.
- Sullo stesso cluster verrà eseguito un solo trial prestazionale alla volta.

Il livello di parallelismo sarà configurabile e i worker potranno essere scalati con Docker Compose.

## Credenziali e artifact sensibili

Il core non gestirà il kubeconfig come caso speciale. Sarà un artifact sensibile prodotto da un plug-in e consumato dagli step successivi.

1. Un plug-in produce un artifact tipizzato e marcato come sensibile.
2. KubePhos lo cifra e ne salva il riferimento.
3. Gli step autorizzati ricevono un handle, non il contenuto persistente.
4. Il worker lo materializza solo durante l'esecuzione.
5. Il dato temporaneo viene eliminato al termine dello step.

## Architettura microkernel

Il core sarà responsabile soltanto di:

- catalogo e versionamento dei plug-in;
- validazione delle pipeline;
- produzione di piani risolti e report di validazione;
- esecuzione del DAG;
- code, scheduling, retry, lock e cancellazione;
- persistenza di stato ed eventi;
- passaggio degli artifact tra gli step;
- gestione dei secret;
- storico, raggruppamento dei risultati e accesso agli artifact;
- rendering generico di form, tabelle e visualizzazioni dichiarative.

Tutte le funzionalità specifiche saranno plug-in, comprese:

- provisioning dell'infrastruttura;
- installazione di Kubernetes;
- gestione del cluster;
- installazione delle applicazioni;
- scheduler, autoscaler e altri componenti;
- generazione del carico;
- fault injection;
- raccolta delle metriche;
- analisi statistica;
- generazione dei grafici;
- esportazione dei risultati.

## Protocollo dei plug-in

Ogni estensione sarà un pacchetto autonomo e dichiarerà:

- tipo e versione del contratto;
- nome e versione;
- JSON Schema della configurazione;
- compatibilità Kubernetes;
- dipendenze;
- capacità offerte;
- immagine OCI e digest;
- modalità di installazione;
- input, output e artifact prodotti;
- condizioni di readiness e cleanup.

Tutti i plug-in useranno lo stesso protocollo:

- `describe`: restituire manifest, schema e capacità;
- `validate`: validare la configurazione;
- `plan`: descrivere operazioni, input e output previsti;
- `execute`: eseguire uno step e produrre artifact tipizzati;
- `status`: restituire avanzamento e stato;
- `cancel`: interrompere l'esecuzione;
- `cleanup`: compensare le modifiche effettuate.

Il protocollo scambierà documenti JSON versionati. Gli artifact voluminosi passeranno attraverso SeaweedFS usando riferimenti temporanei, non attraverso lo standard output.

`validate` e `plan` dovranno essere deterministici e privi di side effect. Un plug-in che non rispetta questa proprietà non potrà essere pubblicato nel catalogo autorizzato.

I plug-in saranno distribuiti come immagini OCI identificate tramite digest. Il worker li eseguirà in modo isolato, applicando permessi, timeout e limiti di risorse dichiarati nel manifest.

## Integrazione dei componenti Kubernetes

L'integrazione avrà due livelli distinti:

1. il plug-in nativo dello scheduler, del descheduler o dell'autoscaler;
2. il pacchetto KubePhos che descrive come costruirlo, configurarlo, installarlo, verificarlo, osservarlo e rimuoverlo.

KubePhos interagirà soltanto con il secondo livello. Non importerà codice proveniente dai framework Kubernetes e non conterrà casi speciali per le singole implementazioni.

### Scheduler

Il punto di partenza sarà un fork o un checkout di `kubernetes-sigs/scheduler-plugins` su un tag compatibile con la versione Kubernetes di destinazione.

Il developer implementerà il plug-in secondo lo Scheduling Framework e lo registrerà nel binario scheduler del proprio repository. Il pacchetto pubblicato conterrà:

- immagine del custom scheduler;
- eventuale immagine del controller associato;
- profilo e schema di configurazione;
- RBAC e CRD richieste;
- compatibilità Kubernetes;
- probe di salute;
- sorgenti dei log;
- test di scheduling di base.

KubePhos installerà il componente come scheduler secondario con un nome distinto. Non sostituirà il default scheduler durante lo sviluppo.

### Descheduler

Il punto di partenza sarà un fork o un checkout di `kubernetes-sigs/descheduler` sul branch o tag corrispondente alla versione Kubernetes di destinazione.

Il developer implementerà e registrerà il plug-in nel framework del descheduler. Il pacchetto pubblicato conterrà:

- immagine del custom descheduler;
- schema della policy;
- modalità supportate, come Job, CronJob o Deployment;
- RBAC richiesto;
- limiti di sicurezza per le eviction;
- probe e test in dry-run;
- sorgenti dei log.

Prima dell'esecuzione reale, KubePhos richiederà la validazione della policy e un test non distruttivo. L'esecuzione iniziale in un workspace di sviluppo userà limiti restrittivi sulle eviction.

### Autoscaler

L'algoritmo di autoscaling sarà separato dal controller che gestisce il ciclo di riconciliazione. Ogni strategia verrà distribuita come immagine indipendente e implementerà un contratto versionato basato su JSON.

Il contratto riceverà:

- riferimento al workload;
- stato e numero corrente di repliche;
- metriche disponibili;
- configurazione validata;
- stato opaco prodotto dall'invocazione precedente.

Il contratto restituirà:

- numero di repliche raccomandato;
- motivazione della decisione;
- metriche utilizzate;
- nuovo stato opaco;
- eventuali warning.

In questo modo una nuova strategia non richiederà modifiche al controller comune o all'immagine di KubePhos e potrà essere implementata in qualsiasi linguaggio.

## Plugin Development Kit

KubePhos fornirà un PDK con template separati per scheduler, descheduler e autoscaler.

```text
kubephos plugin init
kubephos plugin validate
kubephos plugin test
kubephos plugin build
kubephos plugin install
kubephos plugin publish
```

Il PDK si occuperà di:

- generare manifest e JSON Schema;
- generare il boilerplate di registrazione;
- selezionare toolchain e dipendenze compatibili;
- produrre Dockerfile e pacchetto OCI;
- eseguire unit test e test di conformità;
- avviare un test end-to-end su un workspace;
- controllare RBAC, probe, log e cleanup;
- calcolare e registrare il digest;
- produrre la matrice di compatibilità.

Il repository di un'estensione conterrà soltanto il codice specifico e i metadati minimi:

```text
my-plugin/
├── .kubephos/
│   ├── plugin.yaml
│   ├── config.schema.json
│   └── ui.schema.json
├── pkg/
├── tests/
├── go.mod
└── Dockerfile
```

Per i fork upstream esistenti, `kubephos plugin init` aggiungerà soltanto `.kubephos/` e i file di build mancanti senza riorganizzare il repository.

## Developer journey

Lo sviluppatore di un nuovo plug-in seguirà questo flusso:

1. esegue fork o checkout del repository upstream appropriato;
2. seleziona un tag compatibile con il cluster target;
3. implementa il plug-in usando l'interfaccia upstream;
4. esegue `kubephos plugin init` nel repository;
5. descrive configurazione, capacità e probe nel manifest generato;
6. esegue `kubephos plugin validate`;
7. esegue unit test e conformance test con `kubephos plugin test`;
8. costruisce un'immagine locale con `kubephos plugin build`;
9. la installa in un workspace di sviluppo con `kubephos plugin install`;
10. osserva build, installazione, probe e log dalla UI;
11. installa un'applicazione e avvia manualmente il carico;
12. modifica, ricostruisce e sostituisce il plug-in nello stesso workspace;
13. pubblica l'artifact OCI soltanto dopo il superamento dei test.

La modalità development accetterà immagini locali non firmate. I cataloghi condivisi e gli ambienti non development richiederanno digest e firma.

## Build gestita da KubePhos

KubePhos potrà costruire scheduler, descheduler, autoscaler e altri componenti direttamente dalla UI. La build sarà una normale operazione asincrona eseguita da worker dedicati.

```text
Repository o archivio sorgente
           |
           v
Validazione sorgente e manifest
           |
           v
Build isolata
           |
           v
Unit test e conformance test
           |
           v
Immagine OCI + digest
           |
           v
Pubblicazione o import nel cluster
           |
           v
Installazione e health gate
```

Dalla UI lo sviluppatore potrà:

1. indicare repository, commit o tag;
2. scegliere il tipo di plug-in;
3. scegliere la versione Kubernetes target;
4. avviare la validazione;
5. avviare e cancellare la build;
6. seguire i log in tempo reale;
7. eseguire i test;
8. pubblicare l'immagine;
9. installarla nel workspace;
10. ricostruire e sostituire la versione precedente.

Saranno supportate due modalità:

- **managed build**, raccomandata: il PDK genera entrypoint, registrazione, Dockerfile e package metadata;
- **custom build**: il repository fornisce il proprio Dockerfile e i propri comandi, rispettando comunque il contratto degli artifact.

La build non verrà eseguita nel processo API. Un builder isolato riceverà soltanto il sorgente, la configurazione e credenziali temporanee strettamente necessarie. Ogni job avrà timeout, limiti CPU e memoria, filesystem effimero e rete controllata.

KubePhos conserverà per ogni build:

- URL e commit del repository;
- short commit SHA usato come versione leggibile;
- archivio sorgente o relativo hash;
- repository e versione upstream;
- versione Kubernetes target;
- toolchain utilizzata;
- log completi;
- risultati dei test;
- digest dell'immagine;
- metadati di provenienza;
- cache key e artifact prodotti.

Per le build di sviluppo, scheduler e descheduler useranno una versione derivata dallo short commit SHA. Il tag includerà anche la versione Kubernetes target e, quando necessario, l'identificativo della build.

```text
network-aware:git-a1b2c3d4e5f6-k8s1.34
descheduler-custom:git-9f8e7d6c5b4a-k8s1.34
```

Lo short SHA sarà usato soltanto per visualizzazione e tag. La riproducibilità sarà garantita conservando:

- commit SHA completo;
- repository upstream e relativo riferimento;
- hash dell'eventuale source bundle;
- configurazione e toolchain di build;
- digest OCI finale.

Un esperimento farà sempre riferimento al digest OCI e non soltanto al tag. Build differenti dello stesso commit non potranno quindi essere confuse.

SeaweedFS conserverà sorgenti caricati, log, cache e artifact di build. PostgreSQL conserverà stato, metadati e riferimenti.

Per distribuire l'immagine sarà necessario uno dei seguenti meccanismi:

- push verso un registry OCI configurato;
- registry locale opzionale;
- import diretto nel runtime del cluster tramite un plug-in che dichiara tale capacità.

SeaweedFS è un object storage e non sostituisce direttamente un registry OCI. Il metodo di distribuzione verrà validato prima di iniziare la build.

La build stessa sarà implementata tramite plug-in specializzati, per esempio scheduler builder, descheduler builder e autoscaler builder. Il core vedrà soltanto artifact generici come `SourceBundle`, `BuildResult` e `OCIImage`.

## Repository di sviluppo

Scheduler, descheduler e custom pod autoscaler continueranno a essere sviluppati in repository separati. KubePhos non li userà come dipendenze sorgente e non li includerà come submodule.

Saranno disponibili due flussi:

- **Git mode**: KubePhos clona un repository e costruisce un commit preciso;
- **local development mode**: una CLI minimale invia al workspace un source bundle del repository locale e permette di usare un cluster di sviluppo creato da KubePhos.

Il developer potrà ottenere una credenziale Kubernetes temporanea e limitata al workspace. La credenziale amministrativa usata dal framework non verrà esportata.

## Harbor gestito nell'infrastruttura

Harbor sarà il registry OCI predefinito dell'infrastruttura gestita da KubePhos. Non farà parte del deployment Docker Compose della piattaforma e non verrà installato nel cluster Kubernetes. L'utente non selezionerà la versione di Harbor e non dovrà configurarne i dettagli interni.

Il plug-in infrastrutturale creerà o collegherà Harbor e produrrà artifact standard come `RegistryEndpoint` e `RegistryCredential`. Cluster, builder e plug-in consumeranno questi artifact senza dipendere direttamente da Harbor.

Il flusso di una build sarà:

```text
source bundle
    -> build
    -> test
    -> tag con short commit SHA
    -> push su Harbor
    -> acquisizione del digest
    -> installazione tramite digest
```

Progetti Harbor iniziali:

- `kubephos-dev`: immagini temporanee di sviluppo;
- `kubephos-releases`: immagini promosse e immutabili;
- `kubephos-cache`: proxy cache degli artifact upstream.

Le push useranno robot account limitati al singolo progetto. I tag di release saranno immutabili; i run faranno sempre riferimento al digest. Il progetto development avrà quota, retention e garbage collection automatiche.

Una build validata potrà essere promossa da development a release senza essere ricostruita. La promozione manterrà lo stesso digest OCI.

## Servizi dell'infrastruttura

Registry e shared storage faranno parte dell'`Infrastructure` e saranno disponibili prima della creazione o configurazione dei cluster.

```text
Infrastructure
├── compute
├── network
├── Container Registry
│   └── implementazione iniziale: Harbor
├── Shared Storage
│   └── implementazione iniziale: NFS
└── Cluster
```

Il flusso di bootstrap sarà:

```text
provisioning compute e network
        -> provisioning Harbor e NFS
        -> health gate dei servizi infrastrutturali
        -> disponibilità di registry e shared storage
        -> creazione del cluster
        -> collegamento del cluster ai servizi
        -> health gate completo dell'infrastruttura
```

Harbor e NFS potranno essere condivisi da più cluster appartenenti alla stessa infrastruttura. La loro eliminazione sarà impedita finché esistono cluster o build che li utilizzano.

Per infrastrutture importate, il plug-in potrà collegare servizi già esistenti invece di crearne di nuovi, mantenendo gli stessi artifact di output.

## Infrastructure Console

La UI offrirà un unico punto di accesso ai servizi e agli host dell'infrastruttura, senza richiedere all'utente di conoscere altri indirizzi IP o aprire terminali esterni.

Funzioni previste:

- esplorazione di progetti, repository, tag, digest e stato di Harbor;
- upload, download e promozione di artifact OCI tramite operazioni tracciate;
- esplorazione delle share NFS e dei relativi file;
- upload, download, creazione di directory e cancellazione controllata dei dati;
- apertura di sessioni SSH interattive dal browser;
- accesso diretto ai log e ai controlli di salute delle risorse selezionate.

Il frontend non accederà direttamente a Harbor, NFS o SSH. Il backend aprirà sessioni temporanee attraverso plug-in e capability autorizzate, applicherà policy e produrrà un audit log. Le credenziali permanenti non saranno mai restituite al browser.

Le sessioni SSH useranno WebSocket, credenziali effimere quando supportate, timeout di inattività e registrazione di apertura, chiusura e identità della destinazione. La registrazione completa dell'input sarà una policy esplicita perché può contenere dati sensibili.

Le operazioni di modifica saranno distinte dalla consultazione:

- lettura e download potranno essere concessi per capability;
- upload, push e creazione richiederanno permessi di scrittura;
- cancellazioni NFS, rimozione di tag e comandi SSH distruttivi richiederanno conferma esplicita;
- le risorse importate o non marcate come gestite da KubePhos saranno read-only per default;
- tutte le operazioni produrranno eventi consultabili nello storico.

Harbor sarà integrato tramite API e robot account con privilegi minimi. NFS sarà esposto attraverso un file gateway eseguito vicino alla share, evitando mount NFS nel processo API. SSH verrà implementato come plug-in di sessione separato dal processo API.

## Managed stack

I servizi infrastrutturali, i componenti Kubernetes di base e gli strumenti di osservabilità saranno presentati come capacità gestite e non come prodotti da configurare liberamente.

La UI mostrerà concetti come:

- Container Registry;
- Shared Storage;
- Metrics;
- Dashboards;
- Logs.

Le implementazioni iniziali potranno essere Harbor e NFS a livello infrastrutturale, Prometheus e Grafana a livello cluster. I workflow non dipenderanno dai loro nomi.

KubePhos manterrà una distinta versionata del managed stack contenente immagini, chart, digest, compatibilità e configurazioni interne. L'utente non sceglierà le versioni dei singoli componenti.

L'aggiornamento del managed stack sarà associato a una release compatibile del framework. Un aggiornamento dell'interfaccia o una patch del backend non dovrà necessariamente aggiornare i componenti installati. Gli aggiornamenti del managed stack saranno espliciti, pianificati, validati e preceduti da backup quando necessario.

Le versioni installate resteranno visibili per debugging, audit e riproducibilità, pur non essendo modificabili dall'utente.

## Configurazione dei componenti gestiti

Ogni componente avrà tre livelli di configurazione:

1. valori interni fissati e non visibili;
2. profili gestiti con default ragionevoli;
3. pochi parametri funzionali esposti perché significativi per sviluppo o esperimenti.

Esempi di parametri esposti:

- intervallo di scraping;
- query range;
- retention dei dati;
- durata e intensità del carico;
- latenza, banda e packet loss del chaos injector;
- frequenza di campionamento;
- SLO e finestre di aggregazione.

Non saranno esposti tag delle immagini, versioni dei chart, porte interne, configurazioni Helm arbitrarie o password amministrative.

Ogni run conserverà sia i parametri scelti dall'utente sia la configurazione effettiva risultante dai default del managed stack.

## Catalogo delle applicazioni

Le applicazioni saranno pacchetti dichiarativi conformi a un contratto versionato. Il core non conterrà codice dedicato a una specifica applicazione.

Il catalogo avrà due origini:

- **default catalog**: applicazioni curate e distribuite con KubePhos;
- **user catalog**: applicazioni importate tramite UI, API o CLI.

Le applicazioni del catalogo di default saranno conservate sotto `catalog/applications/` e sincronizzate nel database all'avvio. Un'applicazione aggiunta al catalogo di default entrerà nella release successiva di KubePhos.

Le applicazioni importate verranno salvate come bundle immutabili in SeaweedFS, con metadati e digest in PostgreSQL. Una nuova versione produrrà sempre un nuovo bundle e non modificherà i run esistenti.

Formati di importazione iniziali:

- bundle KubePhos;
- manifest Kubernetes;
- chart Helm;
- directory Kustomize.

L'importer eseguirà il discovery dei workload e degli endpoint e presenterà il risultato all'autore per la conferma. Il bundle finale dovrà comunque contenere un inventario esplicito e riproducibile.

## Contratto applicativo

Un pacchetto applicativo dichiarerà:

- identità e versione;
- renderer e file sorgenti;
- schema dei parametri esposti;
- workload;
- capacità dei workload;
- endpoint;
- probe di salute;
- artifact prodotti;
- asset opzionali per workload di carico;
- dipendenze e permessi richiesti.

Ogni workload avrà un identificatore stabile indipendente dal nome Kubernetes e potrà dichiarare capacità come:

- `schedulable`;
- `scalable`;
- `evictable`;
- `observable`;
- `load-target`.

Il riferimento alla risorsa conterrà API version, kind, namespace logico e nome. KubePhos aggiungerà label standard all'istanza e ai Pod template per isolamento, targeting, log e metriche.

```text
kubephos.io/application
kubephos.io/application-instance
kubephos.io/workload
kubephos.io/workspace
kubephos.io/run
```

Il contratto non conterrà configurazioni di scheduler, descheduler o autoscaler specifici.

## Formato di un'applicazione

```text
catalog/applications/online-boutique/
├── application.yaml
├── values.schema.json
├── manifests/
│   └── base.yaml
├── load/
│   └── locustfile.py
└── tests/
    └── smoke.yaml
```

Esempio ridotto del descrittore:

```yaml
apiVersion: applications.kubephos.io/v1alpha1
kind: ApplicationPackage
metadata:
  name: online-boutique
  version: 0.10.5
spec:
  renderer:
    type: manifests
    path: manifests/base.yaml
  workloads:
    - id: frontend
      resource:
        apiVersion: apps/v1
        kind: Deployment
        name: frontend
      capabilities: [schedulable, scalable, evictable, observable]
    - id: redis-cart
      resource:
        apiVersion: apps/v1
        kind: Deployment
        name: redis-cart
      capabilities: [schedulable, observable]
  endpoints:
    - id: frontend-http
      service: frontend
      port: 80
      protocol: http
      loadTarget: true
```

## Migrazione di Online Boutique

Il template attuale verrà separato dalle strategie sperimentali.

Dal manifest applicativo verranno rimossi:

- `schedulerName` hardcoded;
- generazione di HPA e CustomPodAutoscaler;
- immagini e configurazioni del custom pod autoscaler;
- URL Prometheus specifici;
- Redis usato esclusivamente dall'autoscaler;
- node proxy e logica di esposizione specifica del vecchio executor;
- validazione delle configurazioni dei tool esterni.

Il pacchetto Online Boutique conterrà soltanto workload, Service, configurazioni proprie dell'applicazione, PDB, endpoint, probe e asset Locust.

Scheduler, autoscaler, descheduler, esposizione degli endpoint e osservabilità verranno applicati successivamente tramite plug-in e transformer generici.

## Targeting dei workload

Scheduler, descheduler, autoscaler e altri plug-in che agiscono sui workload useranno un contratto comune `TargetBinding`.

Ogni binding dichiarerà:

- capacità richiesta al workload;
- modalità di selezione;
- configurazione condivisa;
- esclusioni;
- configurazioni specifiche per workload.

Modalità di selezione:

- `allEligible`: tutti i workload compatibili;
- `include`: soltanto i workload selezionati;
- `exclude`: tutti i workload compatibili tranne quelli indicati.

La configurazione verrà risolta con questa precedenza:

```text
default del plug-in
    < configurazione condivisa del binding
    < configurazione specifica del workload
```

Esempio concettuale:

```yaml
plugin: custom-autoscaler
targets:
  mode: allEligible
  exclude: [redis-cart]
sharedConfig:
  minReplicas: 1
  maxReplicas: 10
  interval: 15s
overrides:
  frontend:
    maxReplicas: 20
    targetResponseTime: 200ms
```

La UI selezionerà inizialmente tutti i workload compatibili e mostrerà una configurazione condivisa. L'utente potrà deselezionare singoli microservizi o attivare un override soltanto dove necessario.

Il plug-in dichiarerà se opera:

- una volta per binding;
- una volta per workload;
- con configurazione globale e override per target.

La validazione impedirà configurazioni incompatibili, target privi della capacità richiesta e conflitti come due autoscaler attivi sullo stesso workload.

## Migrazioni del database

Le migrazioni PostgreSQL saranno gestite con Liquibase e conservate nella directory `migrations/` del repository.

Docker Compose includerà un job one-shot `migrate`:

```text
postgres healthy
      -> liquibase validate
      -> liquibase update
      -> app e worker
```

L'applicazione non partirà se la migrazione fallisce o se lo schema non è compatibile.

In fase di sviluppo, finché i database sono considerati eliminabili, sarà possibile riscrivere o consolidare i changeset ricreando il database da zero. Non verrà usato `clear-checksums` come flusso ordinario.

Dalla prima release condivisa o persistente, i changeset applicati diventeranno immutabili e le modifiche successive saranno esclusivamente append-only. Per PostgreSQL saranno preferiti changeset SQL formattati, piccoli e transazionali.

## Manutenzione

Le operazioni di manutenzione saranno disponibili dalla UI e tramite pochi comandi CLI. UI e CLI useranno le stesse API quando la piattaforma è disponibile.

Comandi principali:

```text
kubephos status
kubephos doctor
kubephos logs
kubephos backup
kubephos restore
kubephos update
kubephos gc
```

Comandi specialistici:

```text
kubephos db status
kubephos db migrate
kubephos catalog validate
kubephos catalog sync
kubephos infrastructure check
kubephos workspace repair
```

Comportamenti di sicurezza:

- `status` e `doctor` saranno read-only;
- `gc` mostrerà un piano e sarà dry-run per default;
- `update` mostrerà componenti, versioni e migrazioni prima di procedere;
- `backup` includerà metadati, database e riferimenti agli artifact;
- `restore`, `repair` e reset richiederanno conferma esplicita;
- `down` non eliminerà volumi o infrastrutture;
- un comando separato e chiaramente distruttivo sarà disponibile soltanto per il reset dell'ambiente development.

## Artifact di integrazione

Le applicazioni non dovranno conoscere scheduler, descheduler o autoscaler specifici. Comunicheranno attraverso artifact tipizzati:

- `ClusterConnection`;
- `ManifestSet`;
- `WorkloadTargets`;
- `ServiceEndpoints`;
- `SchedulerProfile`;
- `MetricSource`;
- `ComponentInstallation`.

Un transformer generico potrà applicare un `SchedulerProfile` ai PodSpec contenuti in un `ManifestSet`. Autoscaler e descheduler consumeranno `WorkloadTargets` senza richiedere modifiche al pacchetto applicativo.

Ogni run conserverà commit sorgente, riferimento upstream, versione Kubernetes, configurazione risolta e digest delle immagini.

## Composizione delle pipeline

Ogni step dichiarerà soltanto:

- plug-in e versione;
- configurazione;
- artifact richiesti;
- artifact prodotti;
- dipendenze dagli altri step;
- policy di retry e cleanup.

Il motore costruirà il DAG usando i tipi degli artifact. Un plug-in potrà essere sostituito da un altro che accetta e produce gli stessi contratti.

L'aggiunta di qualsiasi funzionalità specifica richiederà soltanto la registrazione di un nuovo plug-in. Non richiederà modifiche o rebuild del core, del backend, del frontend o del modello dati.

## Persistenza

### PostgreSQL

- environment e risorse dichiarate dai plug-in;
- experiment, variant e trial;
- catalogo, manifest e installazioni dei plug-in;
- configurazioni risolte;
- stati, eventi ed errori;
- job, lease e pianificazioni;
- risultati aggregati;
- riferimenti agli artifact.

### SeaweedFS

- log completi;
- metriche raw;
- CSV, JSON e Parquet;
- snapshot delle sorgenti dati;
- plot;
- bundle ZIP esportabili.

I grafici interattivi useranno dati normalizzati. I plot statici saranno prodotti di esportazione e non la fonte primaria dei risultati.

## MVP

- deployment Docker Compose;
- frontend e API nella stessa immagine;
- worker basati sulla stessa immagine;
- microkernel privo di integrazioni specifiche;
- protocollo unico per tutti i plug-in;
- runtime isolato dei plug-in OCI;
- build isolata dei plug-in avviabile dalla UI;
- log live e storico delle build;
- pubblicazione delle immagini tramite digest;
- catalogo delle estensioni versionate;
- installazione di plug-in senza rebuild;
- generazione dinamica dei form tramite JSON Schema;
- pipeline DAG basate su artifact tipizzati;
- validazione completa obbligatoria prima dell'accodamento;
- report degli errori associato ai singoli step;
- piano risolto immutabile e identificato tramite hash;
- health gate obbligatorio dopo ogni step;
- ricontrollo delle dipendenze prima dello step successivo;
- monitoraggio continuo delle condizioni critiche;
- plug-in di riferimento esterni al core;
- gestione cifrata degli artifact sensibili;
- varianti e ripetizioni;
- esecuzione in background;
- scheduling cron;
- retry e cancellazione;
- storico degli esperimenti;
- visualizzazioni prodotte da plug-in di riferimento;
- export prodotti da plug-in di riferimento.

Non fanno parte dell'MVP:

- provider VMware;
- multi-tenancy;
- alta disponibilità della piattaforma;
- marketplace pubblico dei plug-in;
- esecuzione di pacchetti non firmati o non autorizzati.

## Piano d'azione

### Stato di avanzamento

Ultimo aggiornamento: 8 settembre 2026.

| Milestone | Stato | Criterio di completamento |
|---|---|---|
| 1. Fondazioni | Completata | `docker compose up -d` avvia piattaforma, migrazioni e worker; un job validato termina con log live |
| 2. Vertical slice | Completata | Una pipeline composta da plug-in di riferimento produce e visualizza artifact |
| 3. Motore degli esperimenti | In corso | Le esecuzioni sono recuperabili, cancellabili e protette da lease e health gate |
| 4. Confronti e storico | Non iniziata | Due strategie con più ripetizioni sono confrontabili dalla UI |
| 5. Ecosistema dei plug-in | In corso | Un plug-in OCI esterno viene aggiunto senza ricompilare il core |
| 6. Migrazione e consolidamento | Non iniziata | Le integrazioni esistenti usano esclusivamente i contratti pubblici |

La prima iterazione implementa un percorso verticale ridotto ma reale:

1. creazione di un workspace dalla UI;
2. configurazione di un'operazione basata su un plug-in di riferimento;
3. validazione e pianificazione persistenti prima dell'accodamento;
4. conferma esplicita del piano validato;
5. esecuzione con worker concorrenti;
6. precheck e health gate dopo ogni step;
7. cancellazione e log consultabili in tempo reale;
8. verifica end-to-end dello stack Docker Compose.

Il plug-in di riferimento serve soltanto a verificare il protocollo e non introduce nel core concetti specifici di Kubernetes, provider o applicazioni. È già un processo esterno scoperto tramite `plugin.yaml`: il processo API e il worker non lo importano né lo registrano nel codice. Il trasporto iniziale usa JSON su standard input e output e stderr per i log live. Il runner OCI isolato sostituirà l'esecuzione locale nella milestone 5 senza cambiare il modello delle operazioni o l'interfaccia del plug-in.

L'esecuzione come processo locale è ammessa soltanto per plug-in ufficiali inclusi nell'immagine e per lo sviluppo. I plug-in caricati dall'utente dovranno usare il runner OCI, digest immutabile, policy di rete, filesystem effimero e limiti di risorse prima che il caricamento esterno venga abilitato nella UI.

Distinta gestita iniziale:

- PostgreSQL `18.6-alpine3.23`;
- Liquibase `4.32.0`, mantenuto sulla linea 4 perché l'immagine include il driver PostgreSQL;
- SeaweedFS `4.29`;
- Go `1.25.5` per la build dell'immagine applicativa.

PostgreSQL e SeaweedFS non pubblicano porte host per default. L'utente accede ai dati attraverso KubePhos; l'esposizione diretta sarà disponibile soltanto in un profilo diagnostico esplicito per evitare collisioni e ampliare inutilmente la superficie di accesso.

PostgreSQL usa il tag `18.6-alpine3.23`, Liquibase il tag `4.32.0` e SeaweedFS il tag `4.29`. Tutte le immagini del Docker Compose usano tag di versione, aggiornati insieme alla distinta gestita del framework e non configurabili dalla UI.

Il vault locale genera una chiave AES-GCM nel volume privato `platform-data`. Le credenziali vengono validate usando lo schema dichiarato dal plug-in, cifrate prima della persistenza e restituite dalle API soltanto come metadati e fingerprint. Configurazioni, piani, log e artifact conservano esclusivamente riferimenti opachi. Il runtime risolve un riferimento soltanto se il manifest dichiara il permesso `secrets.read:<kind>`.

L'immagine applicativa viene costruita una sola volta dal servizio `app`; il servizio `worker` riusa lo stesso tag e avvia un comando differente.

L'applicazione pubblica la porta HTTP soltanto su `127.0.0.1` per default. Un bind remoto deve essere una scelta esplicita. Prima di supportare accesso multiutente o la Infrastructure Console dovranno essere completati autenticazione, ruoli, autorizzazione per capability, protezione CSRF, audit e gestione delle sessioni. Harbor, NFS e SSH non potranno essere abilitati su un'istanza priva di questi controlli.

Verifiche completate nella prima iterazione:

- compilazione e unit test Go;
- validazione della configurazione Docker Compose;
- migrazione Liquibase su PostgreSQL vuoto e riesecuzione idempotente;
- readiness con PostgreSQL e SeaweedFS obbligatoriamente sani;
- operazione ferma in `ready` prima della conferma del plan hash;
- successo di tutti gli step soltanto dopo precheck e health gate;
- persistenza e streaming dei log;
- produzione, digest e download di un artifact da SeaweedFS;
- discovery del plug-in da descrittore senza registrazione nel core;
- esecuzione del plug-in come processo separato con protocollo JSON e log live;
- verifica all'avvio della corrispondenza tra identità del descrittore e dell'eseguibile;
- generazione del form frontend dal JSON Schema del plug-in senza campi specifici nella UI;
- versioni del managed stack dichiarate esplicitamente; PostgreSQL e SeaweedFS usano tag di versione leggibili;
- fallimento intenzionale con conservazione degli step successivi;
- cancellazione di un'operazione in corso;
- quattro operazioni contemporanee eseguite dal worker pool;
- rendering reale del frontend tramite browser headless.
- accesso API a Proxmox verificato in sola lettura, con il nodo online e tutte le risorse preesistenti classificate come importate e protette;
- arresto controllato e arresto forzato del worker verificati end-to-end: lease scadute e interruzioni terminano in uno stato fallito ispezionabile, senza rieseguire automaticamente step dall'esito incerto.
- creazione di credenziali dalla UI mediante form generato dal manifest, validazione server-side, cifratura e persistenza condivisa tra API e worker;
- verifica negativa che token ID e secret non compaiano in API, configurazioni, piani, log, artifact o dump del database;
- plug-in Proxmox esterno con sole richieste `GET`, validazione dinamica prima della conferma, due health gate e inventory reale completata;
- riavvio di API e worker seguito da una nuova discovery riuscita, a conferma della persistenza e decifratura condivisa della credenziale;
- rendering tramite browser headless della schermata Infrastructure e del vault redatto.

Vincolo operativo corrente: le tre macchine virtuali già presenti sul server Proxmox devono essere ignorate. Il plug-in di discovery le ha rilevate insieme al template e le ha classificate come `imported` e `read-only`; nessuna richiesta di scrittura è stata eseguita. I test futuri di registry, NFS, cluster e componenti gestiti useranno esclusivamente nuove VM con nome, tag e record di ownership KubePhos. Cleanup e modifiche saranno rifiutati per ogni risorsa preesistente o priva di tale ownership.

### 1. Fondazioni

- creare il monorepo;
- definire il modello del dominio;
- preparare l'immagine applicativa;
- creare il deployment Docker Compose;
- introdurre migrazioni PostgreSQL e storage S3;
- implementare la coda persistente dei job.
- definire stati e gate di validazione.

**Risultato:** applicazione avviabile con un solo comando e job di prova eseguito da un worker.

### 2. Vertical slice

- registrare un insieme di plug-in di riferimento;
- costruire una pipeline usando soltanto i loro manifest;
- validare tutti gli step prima di accodare l'esperimento;
- mostrare nella UI il piano e gli errori associati agli step;
- eseguire la pipeline senza logica specifica nel core;
- trasferire artifact normali e sensibili tra gli step;
- salvare e mostrare il risultato.

**Risultato:** primo esperimento completo eseguito dalla UI usando soltanto contratti pubblici.

### 3. Motore degli esperimenti

- workflow persistente;
- step idempotenti;
- worker pool;
- retry e compensazioni;
- lock per cluster;
- concorrenza configurabile;
- cancellazione e cleanup.
- verifica delle post-condizioni dopo ogni step;
- sospensione ispezionabile in caso di errore.

**Risultato:** esecuzioni recuperabili dopo il riavvio della piattaforma.

### 4. Confronti e storico

- varianti e ripetizioni;
- passaggio dei dataset ai plug-in di analisi;
- memorizzazione dei risultati prodotti;
- rendering di visualizzazioni dichiarative;
- cronologia e ricerca;
- download degli artifact di esportazione.

**Risultato:** confronto completo tra almeno due strategie.

### 5. Ecosistema dei plug-in

- protocollo e contratti versionati;
- JSON Schema per i form;
- catalogo delle compatibilità;
- caricamento di qualsiasi plug-in esterno;
- firme, digest, permessi e policy di esecuzione;
- test di conformità per `describe`, `validate`, `plan`, `execute`, `status`, `cancel` e `cleanup`.

**Risultato:** aggiunta di una nuova capacità senza modificare o ricompilare KubePhos.

### 6. Migrazione e consolidamento

- convertire le integrazioni esistenti in plug-in;
- mantenere nel monorepo i plug-in ufficiali senza importarli nel core;
- distribuire ogni plug-in come artifact OCI indipendente;
- mantenere versioni upstream esplicite; PostgreSQL e SeaweedFS usano tag, mentre gli artifact degli esperimenti restano identificati per digest;
- eliminare configurazioni e wrapper duplicati;
- aggiungere test end-to-end e documentazione operativa.

**Risultato:** KubePhos sostituisce il flusso basato sui repository separati.

## Regole del progetto

- Nessun commento esplicativo nel codice.
- Nomi chiari e funzioni piccole.
- Default semplici e opzioni avanzate separate.
- Nessuna funzionalità specifica nel core.
- Tutte le capacità esterne devono essere implementate come plug-in.
- Ogni plug-in deve usare il protocollo pubblico e versionato.
- Nessun esperimento può iniziare senza la validazione completa di tutti gli step.
- Validazione e pianificazione non devono avere side effect.
- Nessuno step può iniziare se le sue dipendenze non sono sane.
- Nessuno step può terminare con successo senza la verifica delle post-condizioni.
- Un exit code positivo non è sufficiente per dichiarare uno step completato.
- I fallimenti devono conservare evidenze e log utili al debugging.
- Configurazioni risolte e versioni sempre conservate.
- Ogni release deve completare almeno un esperimento end-to-end.
- Le operazioni devono essere idempotenti e riprendibili.
- I retry automatici sono consentiti soltanto per step che dichiarano e dimostrano idempotenza; un esito incerto deve richiedere ispezione.
- Il `README.md` deve contenere soltanto prerequisiti, configurazione, avvio, test e comandi principali.
- Architettura, decisioni e specifiche devono stare sotto `docs/`.

## Struttura prevista

```text
kubephos/
├── cmd/
│   └── kubephos/
├── internal/
│   ├── api/
│   ├── domain/
│   ├── experiments/
│   ├── pluginruntime/
│   ├── storage/
│   └── workers/
├── web/
├── plugins/
│   └── reference/
├── catalog/
│   └── applications/
├── contracts/
├── migrations/
├── deploy/
│   └── compose.yaml
├── docs/
├── Dockerfile
├── Makefile
└── README.md
```
