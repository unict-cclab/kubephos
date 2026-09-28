package main

import (
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"kubephos.dev/kubephos/components/mentat/pkg/k8s"
	"kubephos.dev/kubephos/components/mentat/pkg/utils"
)

type config struct {
	probeInterval     time.Duration
	pingAttempts      int
	pingTimeout       time.Duration
	bandwidthPort     int
	bandwidthBytes    int64
	bandwidthInterval time.Duration
	bandwidthJitter   time.Duration
	bandwidthTimeout  time.Duration
}

func positiveInt(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		log.Fatalf("%s must be a positive integer", name)
	}
	return parsed
}

func nonNegativeInt(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		log.Fatalf("%s must be a non-negative integer", name)
	}
	return parsed
}

func loadConfig() config {
	sleepSeconds := positiveInt("SLEEP_SECONDS", 5)
	return config{
		probeInterval:     time.Duration(sleepSeconds) * time.Second,
		pingAttempts:      positiveInt("PING_ATTEMPTS", 5),
		pingTimeout:       time.Duration(positiveInt("PING_TIMEOUT_SECONDS", 1)) * time.Second,
		bandwidthPort:     positiveInt("BANDWIDTH_PORT", 2113),
		bandwidthBytes:    int64(positiveInt("BANDWIDTH_BYTES", 1024*1024)),
		bandwidthInterval: time.Duration(positiveInt("BANDWIDTH_INTERVAL_SECONDS", 90)) * time.Second,
		bandwidthJitter:   time.Duration(nonNegativeInt("BANDWIDTH_JITTER_SECONDS", 30)) * time.Second,
		bandwidthTimeout:  time.Duration(positiveInt("BANDWIDTH_TIMEOUT_SECONDS", 30)) * time.Second,
	}
}

func main() {
	rand.Seed(time.Now().UnixNano())
	configuration := loadConfig()
	hostname, err := k8s.GetNodeName()
	if err != nil {
		log.Fatalf("failed getting node name: %s", err)
	}

	latency := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "node_latency",
		Help:    "ICMP round-trip latency between Kubernetes nodes in seconds.",
		Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
	}, []string{"origin_node", "destination_node"})
	packetLoss := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "node_packet_loss_ratio",
		Help: "Fraction of ICMP probes lost between Kubernetes nodes, from 0 to 1.",
	}, []string{"origin_node", "destination_node"})
	bandwidth := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "node_bandwidth_bytes_per_second",
		Help: "Estimated link bandwidth from origin_node to destination_node in bytes per second, measured from UDP packet dispersion.",
	}, []string{"origin_node", "destination_node"})
	bandwidthFailures := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "node_bandwidth_probe_failures_total",
		Help: "Number of failed inter-node UDP bandwidth probes.",
	}, []string{"origin_node", "destination_node"})
	prometheus.MustRegister(latency, packetLoss, bandwidth, bandwidthFailures)

	go serveBandwidth(configuration)
	go probeICMP(hostname, configuration, latency, packetLoss)
	go probeBandwidth(hostname, configuration, bandwidth, bandwidthFailures)

	metricsServer := &http.Server{
		Addr:              ":2112",
		Handler:           promhttp.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("serving Prometheus metrics on %s", metricsServer.Addr)
	if err := metricsServer.ListenAndServe(); err != nil {
		log.Fatalf("metrics server failed: %s", err)
	}
}

func serveBandwidth(configuration config) {
	address := &net.UDPAddr{Port: configuration.bandwidthPort}
	connection, err := net.ListenUDP("udp", address)
	if err != nil {
		log.Fatalf("bandwidth server failed: %s", err)
	}
	defer connection.Close()
	log.Printf("serving %d-byte UDP bandwidth probes on %s", configuration.bandwidthBytes, address)
	if err := utils.ServeBandwidth(connection, int(configuration.bandwidthBytes)); err != nil {
		log.Fatalf("bandwidth server failed: %s", err)
	}
}

func probeICMP(hostname string, configuration config, latency *prometheus.HistogramVec, packetLoss *prometheus.GaugeVec) {
	for {
		nodes, err := k8s.GetPeerList()
		if err != nil {
			log.Printf("failed getting node list for ICMP probes: %s", err)
			time.Sleep(configuration.probeInterval)
			continue
		}

		for _, node := range nodes {
			if node.Hostname == hostname || node.Ip == "" {
				continue
			}
			result, err := utils.ProbeHost(node.Ip, configuration.pingAttempts, configuration.pingTimeout)
			if err != nil {
				log.Printf("failed probing node %q with ICMP: %s", node.Hostname, err)
				continue
			}
			packetLoss.WithLabelValues(hostname, node.Hostname).Set(result.PacketLossRatio())
			if result.Received > 0 {
				latency.WithLabelValues(hostname, node.Hostname).Observe(result.AverageRTT.Seconds())
			}
		}
		time.Sleep(configuration.probeInterval)
	}
}

func probeBandwidth(hostname string, configuration config, bandwidth *prometheus.GaugeVec, failures *prometheus.CounterVec) {
	time.Sleep(5*time.Second + randomBandwidthJitter(configuration.bandwidthJitter))
	for {
		nodes, err := k8s.GetPeerList()
		if err != nil {
			log.Printf("failed getting node list for bandwidth probes: %s", err)
			time.Sleep(nextBandwidthDelay(configuration))
			continue
		}

		for _, node := range nodes {
			if node.Hostname == hostname || node.Ip == "" {
				continue
			}
			address := net.JoinHostPort(node.Ip, strconv.Itoa(configuration.bandwidthPort))
			bytesPerSecond, err := utils.MeasureBandwidth(address, int(configuration.bandwidthBytes), configuration.bandwidthTimeout)
			if err != nil {
				log.Printf("failed measuring bandwidth to node %q: %s", node.Hostname, err)
				originNode, destinationNode := bandwidthMetricLabels(hostname, node.Hostname)
				bandwidth.WithLabelValues(originNode, destinationNode).Set(0)
				failures.WithLabelValues(originNode, destinationNode).Inc()
				continue
			}
			originNode, destinationNode := bandwidthMetricLabels(hostname, node.Hostname)
			bandwidth.WithLabelValues(originNode, destinationNode).Set(bytesPerSecond)
		}
		time.Sleep(nextBandwidthDelay(configuration))
	}
}

func bandwidthMetricLabels(proberNode, peerNode string) (originNode, destinationNode string) {
	return peerNode, proberNode
}

func nextBandwidthDelay(configuration config) time.Duration {
	return configuration.bandwidthInterval + randomBandwidthJitter(configuration.bandwidthJitter)
}

func randomBandwidthJitter(jitter time.Duration) time.Duration {
	if jitter <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(jitter) + 1))
}
