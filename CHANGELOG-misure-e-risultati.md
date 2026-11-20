# KubePhos — Qualità delle misure e sezione risultati

Branch `fix/critical-data-bugs`, novembre 2026. Il lavoro tocca 4 aree: generazione del carico (Locust), raccolta delle metriche (Prometheus), esecuzione degli esperimenti e comparisons, statistiche e figure della sezione risultati.

**Obiettivo:** far sì che i numeri prodotti da KubePhos siano corretti e confrontabili, e utilizzabili direttamente in pubblicazioni scientifiche, dichiarando sempre da dove viene ogni metrica.

---

## ⚠️ Da leggere prima di tutto: impatto sui dati già raccolti

Alcuni bug corretti hanno distorto le misure degli esperimenti eseguiti finora.

| Condizione dell'esperimento passato | Effetto sui dati |
|---|---|
| Load session con più di 1 replica Locust | Il carico reale era **N volte** quello configurato: ogni replica generava da sola tutto l'RPS. |
| RPS non intero (es. 9,5) o molti utenti rispetto all'RPS | Il pacer poteva inviare **fino al doppio** delle richieste configurate. |
| Fasi con valore 0 (RPS 0, `startRps` 0, curva 0) | Errore nello script Locust, oppure rampa esponenziale eseguita con curva 3 invece che lineare. |
| CPU, memoria, riavvii, repliche pronte | Includevano anche i pod di Locust (il generatore di carico). |
| Statistiche delle singole run | Includevano campioni precedenti all'avvio del carico, mentre i grafici no. |
| "Pod restarts" | Era una media nel tempo di un contatore cumulativo, non il numero di riavvii durante la run. |
| Finestra di misura | Locust partiva **prima** dell'inizio registrato, e gli ultimi campioni andavano persi. |

Il frontend corregge già, sui dataset vecchi, i punti che si possono ricalcolare: esclusione di Locust, taglio alla finestra del carico, riavvii. Il carico effettivamente generato invece non si può correggere a posteriori. **Gli esperimenti eseguiti con più repliche o con RPS non interi vanno rieseguiti.**

---

## 1. Generazione del carico (plugin `load-session`)

- **Carico diviso tra le repliche.** Il profilo configurato è il carico *totale*: ogni replica Locust genera RPS ÷ repliche (`KUBEPHOS_LOAD_WORKERS`).
- **Pacer corretto.** Non supera più il target, anche con RPS frazionari, molti utenti o variazioni di rate. Non invia più richieste dopo la fine del profilo.
- **Partenza sincronizzata.** Quando tutti i pod sono pronti, KubePhos pubblica un istante di avvio assoluto e tutti i worker partono insieme.
  - `startedAt` e `completedAt` sono esattamente t=0 e t=fine del profilo (`clock: "shared-start"`).
  - Uno sfasamento tra l'orologio dell'host KubePhos e quello del cluster viene misurato e compensato, fino a ±30 s.
  - Se i nodi del cluster non sono sincronizzati tra loro (più di 2 s di differenza), il run viene rifiutato con un messaggio chiaro.
- **Warmup** (nuovo campo `warmupSeconds`, 0–3600, default 0). Prima del profilo misurato viene inviato il rate iniziale; le statistiche Locust vengono azzerate all'inizio della misura.
- **Drain** (nuovo campo `drainSeconds`, 0–600, default 30). A fine profilo i pod restano attivi senza traffico, così Prometheus raccoglie gli ultimi valori prima dello spegnimento.
- **Statistiche native di Locust** salvate nel risultato (`locustSummary`):
  - per endpoint e in aggregato: richieste, errori, media, min/max, P50/P90/P95/P99/P99.9 esatti (istogrammi dei worker uniti, non medie di percentili);
  - tabella degli errori;
  - avvisi di CPU del generatore.
- **Utenti adattivi: carico davvero "a ciclo aperto".**
  - Prima ogni worker avviava utenti pari al suo RPS. Ogni utente fa circa una richiesta per ogni intervallo di latenza, quindi oltre 1 s di latenza media il generatore restava indietro: l'app riceveva meno carico proprio quando andava in saturazione.
  - Ora gli utenti seguono la latenza osservata: utenti = RPS × latenza × 1,5 (legge di Little), con minimo pari all'RPS e massimo pari a "Maximum users per worker". La stima sale subito quando la latenza cresce e scende lentamente, così il numero di utenti non oscilla.
  - Verificato con Locust 2.42 reale contro un server con latenza di 2 s, target 10 RPS. Prima: circa 5 RPS ottenuti. Ora: 10 RPS, perché gli utenti passano automaticamente da 10 a 31.
- **Rilevazione del "coordinated omission".** Quando la latenza sale e Locust non riesce a rispettare il ritmo previsto, ora viene misurato: richieste in ritardo e ritardo medio e massimo.
- **Nuove serie Prometheus di Locust:**
  - etichetta `name` per endpoint, con un limite di 50 nomi distinti;
  - target RPS, utenti attivi, ritardo dello scheduling e fase (warmup/misura/drain).

## 2. Raccolta delle metriche (plugin `metrics-collector`)

- **Fonte di ogni metrica.** Il dataset contiene un catalogo (`spec.metrics`) con, per ogni metrica:
  - la fonte: Locust, Istio, cAdvisor, kube-state-metrics, Mentat, Cilium o KubePhos;
  - il punto di osservazione: lato client, lato server, risorse, rete;
  - la query PromQL esatta, la finestra usata e una descrizione.

  Esempio: *P95 lato client (Locust)* e *P95 lato server (Istio)* sono ora chiaramente distinti.
- **Finestra di `rate()` configurabile** (nuovo campo `rateWindow`: `auto`, 30s, 1m, 2m, 5m). Prima era sempre fissa a 1 minuto.
  - `auto` usa il valore maggiore tra la risoluzione e 4 volte l'intervallo di scrape, rilevato da Prometheus.
  - Una finestra più corta di 2 scrape viene allargata automaticamente.
- **Nuove metriche:**
  - P50/P90/P99 e media della latenza, sia lato client (Locust) sia lato server (Istio);
  - RPS offerto, RPS ottenuto, ritardo di scheduling, utenti;
  - repliche pronte di StatefulSet e DaemonSet.
- **% errori senza traffico ora risulta *non disponibile*,** invece dello 0% che si otteneva prima.
- **Contesto del run** (`spec.runContext`), raccolto con il massimo sforzo possibile, senza mai far fallire il run:
  - versioni di Prometheus, Kubernetes e Istio;
  - nodi con zona, regione, tipo di istanza, kubelet, runtime e kernel;
  - intervallo di scrape;
  - warmup e drain applicati;
  - **altri generatori di carico KubePhos attivi in contemporanea sullo stesso cluster**.
- **Raggruppamento dei pod per servizio:** l'appartenenza viene risolta dai metadati del cluster (etichette `workload`, `workload_kind`, `node`, `zone`) e non più indovinata dal nome del pod. Prima, ad esempio, `cart-db-0` veniva attribuito a `cart` e i DaemonSet venivano ignorati.
- **Metriche facoltative:** se una delle nuove metriche manca (immagine Locust vecchia, nessun traffico), il run non fallisce più.
- **Dashboard Grafana:** anche lì Locust è escluso dai pannelli dei workload.

## 3. Esecuzione degli esperimenti (backend)

- **Un errore nella pulizia non butta più via un risultato valido.**
  - La ripetizione resta "succeeded" con il suo dataset e un avviso (`cleanupError`).
  - Le ripetizioni successive vengono comunque annullate, per non misurare su un ambiente sporco.
- **Pausa tra le ripetizioni** (`restSecondsBetweenTrials`, 0–3600 s). Il cluster può tornare a regime prima della ripetizione successiva.
- **Prima ripetizione come warm-up** (`firstTrialPilot`). Assorbe il download delle immagini e le cache fredde. È visibile come "Warm-up (excluded)" ed è esclusa da statistiche, grafici e comparisons.
- **Migrazione `059-measurement-integrity.sql`.** È solo additiva: nuove colonne e una nuova tabella, i dati esistenti non vengono toccati.

## 4. Comparisons

- **Snapshot congelato.** Alla creazione, alla modifica o al refresh, la comparison salva le run e i dataset esatti. I numeri non cambiano più se gli esperimenti sorgente vengono rieseguiti.
  - Se le sorgenti cambiano, appare l'avviso "stale" con il pulsante **Refresh snapshot**.
  - Le comparisons create prima di questa modifica restano "Live" finché non vengono salvate o aggiornate.
- **Controllo di compatibilità** dei profili di carico e di raccolta tra gli esperimenti confrontati:
  - scenario, step, durata, warmup, repliche, utenti, spawn rate, fasi di rete;
  - risoluzione, finestra di `rate`, SLO.

  Le differenze sono mostrate in una tabella. È un avviso: la comparison non viene bloccata.

## 5. Statistiche (nuovo modulo `statistics.ts`)

- **Valore critico della t di Student esatto** per qualsiasi numero di gradi di libertà. Prima i valori erano approssimati oltre 30 gradi di libertà. È verificato rispetto a SciPy.
- **Intervalli di confidenza di una singola run corretti per l'autocorrelazione.** I campioni vicini nel tempo sono correlati, quindi l'IC usa un numero di campioni effettivo (modello AR(1)); prima l'IC risultava troppo stretto. Le tabelle mostrano n e n_eff.
- **Confronto tra varianti** contro una baseline scelta oppure tutte le coppie, per qualsiasi numero di varianti. Per ogni confronto:
  - differenza delle medie, differenza percentuale e suo IC 95% (Welch);
  - p-value di Welch;
  - **p corretto con Holm** per confronti multipli;
  - **Hedges' g** con IC (Cohen's d corretto per campioni piccoli);
  - Mann–Whitney come test non parametrico;
  - segnalazione se n < 3 o se la varianza è nulla.
- **Valori non disponibili:** con n < 2 l'IC è mostrato come "n/a". I dati mancanti non sono mai trattati come zero.
- **Esportazione CSV:** sono state aggiunte le colonne fonte, punto di osservazione, IC e n_eff, oltre all'export della tabella dei confronti.

## 6. Interfaccia dei risultati e figure da pubblicazione

- **Nuove schede:** Statistics · Load generator · Plots · Context & methods · Placement · Export.
- **Badge della fonte** su ogni metrica (es. "Locust · client side", "Istio · server side"). Cliccando si vedono la descrizione, la query PromQL e la finestra.
- **Avvisi sulla qualità della misura:**
  - RPS ottenuto sotto il 95% di quello offerto;
  - richieste in ritardo;
  - generatore di carico saturo (CPU);
  - altri test in contemporanea sullo stesso cluster.
- **Scheda Load generator:** tabella per endpoint (percentili esatti di Locust, media ± IC tra le ripetizioni), tabella degli errori, grafico RPS offerto vs ottenuto.
- **Context & methods:** contesto del run e un **paragrafo "Methods" generato automaticamente**, da copiare nel paper (ripetizioni, warmup, durata, scrape, finestra, fonti dei percentili, metodi statistici).
- **Impostazioni delle figure**, salvate per pagina:
  - dimensione: colonna singola 3,5″, 1,5 colonne, colonna doppia 7,16″, slide 16:9;
  - font serif/sans e dimensione in punti;
  - incertezza: nessuna, ±SD o IC 95%;
  - asse Y da zero, scala logaritmica;
  - modalità adatta alla stampa in bianco e nero (palette Okabe–Ito, tratteggi, marker).
- **Nuovi tipi di figura:**
  - dot plot delle singole ripetizioni con media e IC, al posto di barre ± SD;
  - scala dei percentili P50→P99, client vs server;
  - RPS offerto vs ottenuto.
- **Export:**
  - PDF con testo vero e font standard;
  - SVG valido con dimensioni in pollici e didascalia/fonte nei metadati;
  - PNG a 300/600 dpi;
  - CSV dei dati della figura.

  È stato corretto anche un bug per cui gli SVG esportati non si aprivano in altri programmi.
- **Dimensionamento del generatore nella configurazione** (Experiments → Configurations → Load profile → scheda *Generator*). Il pannello "Generator sizing" calcola il picco del profilo e confronta i valori attuali con quelli consigliati:
  - **worker:** circa 150 RPS per worker, perché ogni worker Locust usa un solo core;
  - **max users per worker:** quanti servono a reggere il target fino a 2 s di latenza media;
  - **spawn rate:** in base alla rampa più ripida del profilo;
  - **warmup:** almeno 60 s.

  Spiega ogni avviso e ha un pulsante **Apply recommended values**. Esempio: con 350 RPS consiglia 3 worker, spawn rate 12/s e warmup 60 s.
- **Dialogo di esecuzione:** nuovi campi "Rest between repetitions (s)" e "First repetition is a warm-up".
- **Lista delle run:** badge "Warm-up (excluded)", "Resting until …" e "Result kept — cleanup failed".

## 6-bis. Mix delle richieste e catena dei servizi

**Problema:** con 350 req/s su Locust non si sapeva quante richieste arrivano a ogni servizio interno. Per esempio, una pagina prodotto di Online Boutique chiama productcatalog, currency (una volta per ogni prezzo), ad e recommendation.

- **Mix configurabile** (Configurations → Load profile → scheda *Request mix*):
  - KubePhos legge i task dal locustfile dello scenario, nelle varie forme: `tasks = {...}`, liste, `@task(n)`, TaskSet annidati;
  - per ognuno mostra le chiamate HTTP (metodo + path) e il % di default, modificabile con campo e slider;
  - riporta la quota di richieste HTTP, il riepilogo GET/POST e un pulsante per tornare ai default del locustfile.
  - Locust applica i pesi a runtime (`taskWeights`) e conta le esecuzioni di ogni task. Il mix effettivo viene salvato nel risultato (`taskMix`).
- **Misura della catena** (opzione *Measure call chain*): prima del warmup, ogni task gira da solo per 60 s (configurabile) a pochi RPS. Il collector conta da Istio le richieste ricevute da ogni servizio e su ogni arco servizio→servizio. Il risultato è il modello `callModel`: chiamate per esecuzione di ogni task.
  - Costa tasks × 60 s una volta per applicazione/versione; per Online Boutique circa 6 minuti.
- **Previsione grafica:** accanto al mix compaiono il grafo dei servizi con le req/s previste al picco del profilo e per pod, le etichette sugli archi e una classifica dei servizi più colpiti. La previsione usa l'ultimo modello misurato per la stessa applicazione e scenario; senza modello il grafo indica "Not measured yet" e spiega come attivare la misura.
- **Risultati:** nuova scheda *Call chain* con il mix effettivo, la tabella per task e il confronto tra req/s previste e misurate per servizio.
- **Avanzamento della run:** mostra le fasi "Measuring the call chain", poi "Warmup", poi "Load running".
- **Comparisons:** segnalano anche mix di richieste diversi tra gli esperimenti.

## 7. Nuove opzioni di configurazione (tutte facoltative, retrocompatibili)

| Dove | Campo | Default |
|---|---|---|
| Load session | `warmupSeconds` | 0 |
| Load session | `drainSeconds` | 30 |
| Metrics collector | `rateWindow` | `auto` |
| Esecuzione esperimento | `restSecondsBetweenTrials` | 0 |
| Esecuzione esperimento | `firstTrialPilot` | false |
| Load session | `taskWeights` (mix delle richieste) | pesi del locustfile |
| Load session | `profileCallChain` / `profileSecondsPerTask` / `profileRps` | false / 60 / min(10, RPS iniziale) |

**Impostazioni consigliate per run da pubblicazione:**
- almeno 5 ripetizioni con warm-up attivo;
- pausa tra le ripetizioni di almeno 60 s;
- `warmupSeconds` di almeno 60;
- `rateWindow` impostato su `auto`.

Le versioni dei plugin (`plugin.yaml`) non sono state incrementate: va fatto al momento della release.

## 8. Limiti noti e punti da verificare su un cluster reale

- **Immagini Locust:** devono contenere `/bin/sh`, `date` e `python3`. Servono per la partenza sincronizzata e per leggere le statistiche (`kubectl exec`). Le immagini dei profili attuali lo sono, ma va confermato.
- **Statistiche Locust non lette** (permessi o mTLS): il run riesce comunque e nei log compare "Locust statistics summary is unavailable".
- **Intervallo di scrape non rilevabile:** la finestra torna a 1 minuto.
- **`deployment.ready_replicas` resta obbligatoria.** Un'applicazione HTTP personalizzata senza alcun Deployment farebbe fallire la raccolta. Tutte le applicazioni del catalogo hanno Deployment.
- **Percentili lato server:** sono la media tra i workload di destinazione, ed è indicato in didascalia.
- **Rampe:** nell'avviso RPS offerto vs ottenuto il confronto tra un valore istantaneo e un `rate()` può dare falsi avvisi.
- **Margine del bundle iniziale:** 218,9 KiB su un limite di 220 KiB.
- **Lettura del locustfile:** è statica. Pesi calcolati a runtime o cicli vengono letti come scritti, con un avviso. Le richieste fatte in `on_start` (es. il login di Train Ticket) finiscono nella prima finestra di misura della catena.
- **Avanzamento con il mix di default:** con la misura della catena attiva e il mix di default, la barra di avanzamento non conosce il numero di task e può anticipare.
- **Limite di capacità per worker:** i 150 RPS per worker sono un valore prudente per `HttpUser`. Con `FastHttpUser` un worker regge di più; la conferma va presa dalla run pilota (nessun avviso CPU nella scheda Load generator).

## 9. Test

- **Go** (`go vet` e `go test`):
  - tutti i plugin e `internal/...`;
  - integrazioni con PostgreSQL 18.6, come in CI: storage, API, engine, migrazione 058;
  - plugin `harbor` escluso: il suo test fallisce solo per l'assenza di `jq` nel container.
- **Frontend:**
  - 742 test Vitest;
  - `tsc -b`, build, `check:bundle`, `check:theme`, `npm audit` (0 vulnerabilità).
- **Test aggiunti** tra gli altri:
  - simulazione a eventi discreti del pacer con utenti concorrenti;
  - wrapper Locust eseguito con Locust 2.42 reale;
  - valori statistici confrontati con SciPy;
  - esecuzione completa load-session → collector con runner finti;
  - dataset lunghi (oltre 150k punti) e dataset vecchi.
