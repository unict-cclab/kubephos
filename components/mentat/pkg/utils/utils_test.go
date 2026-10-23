package utils

import (
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"
)

func TestPacketLossRatio(t *testing.T) {
	result := PingResult{Sent: 5, Received: 3}
	if got, want := result.PacketLossRatio(), 0.4; got != want {
		t.Fatalf("PacketLossRatio() = %v, want %v", got, want)
	}
}

func TestBandwidthPacketTrain(t *testing.T) {
	server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- ServeBandwidth(server, 1024*1024) }()

	bytesPerSecond, err := MeasureBandwidth(server.LocalAddr().String(), 1024*1024, 2*time.Second)
	if err != nil {
		t.Fatalf("MeasureBandwidth() error = %v", err)
	}
	if bytesPerSecond <= 0 {
		t.Fatalf("MeasureBandwidth() = %v, want positive throughput", bytesPerSecond)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("ServeBandwidth() error = %v, want closed connection", err)
	}
}

func TestBandwidthPayloadValidation(t *testing.T) {
	if _, err := MeasureBandwidth("127.0.0.1:2113", bandwidthPayloadBytes, time.Second); err == nil {
		t.Fatal("MeasureBandwidth() accepted a one-packet payload")
	}
	connection, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := ServeBandwidth(connection, bandwidthPayloadBytes); err == nil {
		t.Fatal("ServeBandwidth() accepted a one-packet payload")
	}
}

func TestProbeHostRejectsInvalidInput(t *testing.T) {
	if _, err := ProbeHost("not-an-ip", 3, time.Second); err == nil {
		t.Fatal("ProbeHost() accepted an invalid IP")
	}
	if _, err := ProbeHost("127.0.0.1", 0, time.Second); err == nil {
		t.Fatal("ProbeHost() accepted zero attempts")
	}
}

// Exercise missing completion, reordered completion, and insufficient samples.
func TestBandwidthCompletionLoss(t *testing.T) {
	for _, tc := range []struct {
		name        string
		sequences   []uint32
		markerFirst bool
		wantError   bool
	}{
		{"all data without marker", []uint32{0, 1, 2, 3}, false, false},
		{"partial data without marker", []uint32{0, 1, 3}, false, false},
		{"marker before data", []uint32{0, 1, 2, 3}, true, false},
		{"insufficient data", []uint32{0}, false, true},
		{"duplicate data", []uint32{0, 0, 0}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			done := make(chan struct{})
			defer func() { <-done }()
			go func() {
				defer close(done)
				request := make([]byte, bandwidthHeaderBytes)
				_, peer, err := server.ReadFromUDP(request)
				if err != nil {
					return
				}
				if tc.markerFirst {
					binary.BigEndian.PutUint32(request[12:16], 4)
					server.WriteToUDP(request, peer)
				}
				for _, seq := range tc.sequences {
					packet := make([]byte, bandwidthDatagramBytes)
					copy(packet, request)
					binary.BigEndian.PutUint32(packet[12:16], seq)
					server.WriteToUDP(packet, peer)
					time.Sleep(time.Millisecond)
				}
			}()
			value, err := MeasureBandwidth(server.LocalAddr().String(), 4*bandwidthPayloadBytes, 150*time.Millisecond)
			if tc.wantError {
				if err == nil {
					t.Fatalf("accepted insufficient data: %v", value)
				}
			} else if err != nil || value <= 0 {
				t.Fatalf("valid data rejected: %v %v", value, err)
			}
		})
	}
}
