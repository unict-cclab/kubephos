package utils

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"github.com/digineo/go-ping"
)

const (
	bandwidthHeaderBytes   = 20
	bandwidthDatagramBytes = 1200
	bandwidthPayloadBytes  = bandwidthDatagramBytes - bandwidthHeaderBytes
	bandwidthDoneCopies    = 3
)

var bandwidthMagic = [4]byte{'K', 'B', 'P', '1'}

type PingResult struct {
	AverageRTT time.Duration
	Sent       int
	Received   int
}

func (result PingResult) PacketLossRatio() float64 {
	if result.Sent == 0 {
		return 0
	}
	return float64(result.Sent-result.Received) / float64(result.Sent)
}

func ProbeHost(destinationIP string, attempts int, timeout time.Duration) (PingResult, error) {
	result := PingResult{Sent: attempts}
	if attempts <= 0 {
		return PingResult{}, fmt.Errorf("attempts must be positive")
	}

	remoteIP := net.ParseIP(destinationIP)
	if remoteIP == nil {
		return PingResult{}, fmt.Errorf("invalid destination IP %q", destinationIP)
	}

	pinger, err := ping.New("0.0.0.0", "")
	if err != nil {
		return PingResult{}, err
	}
	defer pinger.Close()

	remoteAddr := &net.IPAddr{IP: remoteIP}
	var totalRTT time.Duration
	for i := 0; i < attempts; i++ {
		rtt, pingErr := pinger.Ping(remoteAddr, timeout)
		if pingErr != nil {
			continue
		}
		result.Received++
		totalRTT += rtt
	}

	if result.Received > 0 {
		result.AverageRTT = totalRTT / time.Duration(result.Received)
	}
	return result, nil
}

// ServeBandwidth serves UDP packet trains. Measuring dispersion between the
// first and last packet excludes fixed propagation delay, while a bandwidth
// shaper still changes the spacing between packets.
func ServeBandwidth(connection *net.UDPConn, maximumBytes int) error {
	if maximumBytes < 2*bandwidthPayloadBytes {
		return fmt.Errorf("bandwidth payload must be at least %d bytes", 2*bandwidthPayloadBytes)
	}
	maximumPackets := uint32((maximumBytes + bandwidthPayloadBytes - 1) / bandwidthPayloadBytes)
	request := make([]byte, bandwidthHeaderBytes)
	for {
		n, peer, err := connection.ReadFromUDP(request)
		if err != nil {
			return err
		}
		if n != bandwidthHeaderBytes || !validBandwidthMagic(request) {
			continue
		}
		nonce := binary.BigEndian.Uint64(request[4:12])
		packetCount := binary.BigEndian.Uint32(request[16:20])
		if packetCount < 2 || packetCount > maximumPackets {
			continue
		}

		packet := make([]byte, bandwidthDatagramBytes)
		copy(packet[:4], bandwidthMagic[:])
		binary.BigEndian.PutUint64(packet[4:12], nonce)
		binary.BigEndian.PutUint32(packet[16:20], packetCount)
		for sequence := uint32(0); sequence < packetCount; sequence++ {
			binary.BigEndian.PutUint32(packet[12:16], sequence)
			if _, err := connection.WriteToUDP(packet, peer); err != nil {
				break
			}
		}

		// A repeated, header-only completion marker lets the receiver finish
		// promptly even when the final data packet is lost.
		completion := packet[:bandwidthHeaderBytes]
		binary.BigEndian.PutUint32(completion[12:16], packetCount)
		for copyIndex := 0; copyIndex < bandwidthDoneCopies; copyIndex++ {
			_, _ = connection.WriteToUDP(completion, peer)
		}
	}
}

func MeasureBandwidth(address string, payloadBytes int, timeout time.Duration) (float64, error) {
	if payloadBytes < 2*bandwidthPayloadBytes {
		return 0, fmt.Errorf("bandwidth payload must be at least %d bytes", 2*bandwidthPayloadBytes)
	}
	peer, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return 0, err
	}
	connection, err := net.DialUDP("udp", nil, peer)
	if err != nil {
		return 0, err
	}
	defer connection.Close()
	_ = connection.SetReadBuffer(4 * 1024 * 1024)
	if err := connection.SetDeadline(time.Now().Add(timeout)); err != nil {
		return 0, err
	}

	packetCount := uint32((payloadBytes + bandwidthPayloadBytes - 1) / bandwidthPayloadBytes)
	request := make([]byte, bandwidthHeaderBytes)
	copy(request[:4], bandwidthMagic[:])
	nonceBytes := request[4:12]
	if _, err := rand.Read(nonceBytes); err != nil {
		return 0, err
	}
	nonce := binary.BigEndian.Uint64(nonceBytes)
	binary.BigEndian.PutUint32(request[16:20], packetCount)
	if _, err := connection.Write(request); err != nil {
		return 0, err
	}

	buffer := make([]byte, bandwidthDatagramBytes)
	var firstSequence, highestSequence uint32
	var firstArrival, highestArrival time.Time
	receivedData := false
	for {
		n, err := connection.Read(buffer)
		arrival := time.Now()
		if err != nil {
			return 0, err
		}
		if n < bandwidthHeaderBytes || !validBandwidthMagic(buffer[:n]) || binary.BigEndian.Uint64(buffer[4:12]) != nonce || binary.BigEndian.Uint32(buffer[16:20]) != packetCount {
			continue
		}
		sequence := binary.BigEndian.Uint32(buffer[12:16])
		if sequence == packetCount {
			break
		}
		if n != bandwidthDatagramBytes || sequence >= packetCount {
			continue
		}
		if !receivedData {
			firstSequence, highestSequence = sequence, sequence
			firstArrival, highestArrival = arrival, arrival
			receivedData = true
			continue
		}
		if sequence > highestSequence {
			highestSequence = sequence
			highestArrival = arrival
		}
	}
	if !receivedData || highestSequence <= firstSequence {
		return 0, fmt.Errorf("bandwidth probe returned insufficient data")
	}
	elapsed := highestArrival.Sub(firstArrival)
	if elapsed <= 0 {
		return 0, fmt.Errorf("bandwidth probe returned an invalid duration")
	}
	// Use the sequence span rather than received packet count so independent
	// packet loss does not masquerade as reduced link bandwidth.
	logicalBytes := uint64(highestSequence-firstSequence) * bandwidthPayloadBytes
	return float64(logicalBytes) / elapsed.Seconds(), nil
}

func validBandwidthMagic(packet []byte) bool {
	return len(packet) >= 4 && packet[0] == bandwidthMagic[0] && packet[1] == bandwidthMagic[1] && packet[2] == bandwidthMagic[2] && packet[3] == bandwidthMagic[3]
}
