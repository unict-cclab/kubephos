package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"kubephos.dev/kubephos/components/mentat/pkg/utils"
)

type meshCluster struct {
	Name    string `json:"name"`
	PodCIDR string `json:"podCIDR"`
}
type meshPeer struct{ cluster, address string }

func meshClusters(raw string) ([]meshCluster, error) {
	var clusters []meshCluster
	if err := json.Unmarshal([]byte(raw), &clusters); err != nil {
		return nil, err
	}
	if len(clusters) < 2 || len(clusters) > 6 {
		return nil, fmt.Errorf("mesh requires two to six cluster networks")
	}
	for _, cluster := range clusters {
		if _, network, err := net.ParseCIDR(cluster.PodCIDR); err != nil || network.IP.To4() == nil || cluster.Name == "" {
			return nil, fmt.Errorf("invalid mesh cluster network")
		}
	}
	return clusters, nil
}
func meshPeerCluster(address string, clusters []meshCluster) string {
	ip := net.ParseIP(address)
	for _, cluster := range clusters {
		_, network, err := net.ParseCIDR(cluster.PodCIDR)
		if err == nil && network.Contains(ip) {
			return cluster.Name
		}
	}
	return ""
}
func resolveMeshPeers(origin string, clusters []meshCluster) ([]meshPeer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, "mentat-global.kubephos-observability.svc.cluster.local")
	if err != nil {
		return nil, err
	}
	result := []meshPeer{}
	seen := map[string]bool{}
	for _, address := range addresses {
		ip := address.IP.String()
		cluster := meshPeerCluster(ip, clusters)
		if cluster != "" && cluster != origin && !seen[ip] {
			result = append(result, meshPeer{cluster: cluster, address: ip})
			seen[ip] = true
		}
	}
	return result, nil
}
func startMeshProbes(hostname string, configuration config) {
	origin := os.Getenv("CLUSTER_NAME")
	raw := os.Getenv("MESH_CLUSTERS")
	if origin == "" || raw == "" {
		return
	}
	clusters, err := meshClusters(raw)
	if err != nil {
		log.Printf("mesh probes disabled: %v", err)
		return
	}
	labels := []string{"origin_cluster", "destination_cluster", "origin_node", "destination_node"}
	latency := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "cluster_network_latency", Help: "ICMP RTT between pod networks of connected Cilium clusters, in seconds.", ConstLabels: prometheus.Labels{"measurement_cluster": origin}, Buckets: prometheus.ExponentialBuckets(.0001, 2, 18)}, labels)
	loss := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "cluster_network_packet_loss_ratio", Help: "Fraction of ICMP probes lost between connected clusters.", ConstLabels: prometheus.Labels{"measurement_cluster": origin}}, labels)
	bandwidth := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "cluster_network_bandwidth_bytes_per_second", Help: "UDP packet dispersion bandwidth between connected clusters, in bytes per second.", ConstLabels: prometheus.Labels{"measurement_cluster": origin}}, labels)
	prometheus.MustRegister(latency, loss, bandwidth)
	go func() {
		for {
			peers, err := resolveMeshPeers(origin, clusters)
			if err != nil {
				log.Printf("resolve mesh probes: %v", err)
			}
			var group sync.WaitGroup
			slots := make(chan struct{}, 4)
			for _, peer := range peers {
				peer := peer
				group.Add(1)
				go func() {
					defer group.Done()
					slots <- struct{}{}
					defer func() { <-slots }()
					result, err := utils.ProbeHost(peer.address, configuration.pingAttempts, configuration.pingTimeout)
					if err != nil {
						log.Printf("mesh ICMP probe to %s: %v", peer.cluster, err)
						return
					}
					loss.WithLabelValues(origin, peer.cluster, hostname, peer.address).Set(result.PacketLossRatio())
					if result.Received > 0 {
						latency.WithLabelValues(origin, peer.cluster, hostname, peer.address).Observe(result.AverageRTT.Seconds())
					}
				}()
			}
			group.Wait()
			time.Sleep(configuration.probeInterval)
		}
	}()
	go func() {
		time.Sleep(5*time.Second + randomBandwidthJitter(configuration.bandwidthJitter))
		for {
			peers, err := resolveMeshPeers(origin, clusters)
			if err != nil {
				log.Printf("resolve mesh bandwidth: %v", err)
			}
			for _, peer := range peers {
				measured, err := utils.MeasureBandwidth(net.JoinHostPort(peer.address, strconv.Itoa(configuration.bandwidthPort)), int(configuration.bandwidthBytes), configuration.bandwidthTimeout)
				if err != nil {
					bandwidth.WithLabelValues(peer.cluster, origin, peer.address, hostname).Set(math.NaN())
					continue
				}
				bandwidth.WithLabelValues(peer.cluster, origin, peer.address, hostname).Set(measured)
			}
			time.Sleep(nextBandwidthDelay(configuration))
		}
	}()
}
