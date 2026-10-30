// Package connectivity performs bounded, read-only checks of external services.
package connectivity

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/kubeconfig"
)

func Result(name, status, message string) domain.ConnectivityTarget {
	return domain.ConnectivityTarget{Name: name, Status: status, Message: message}
}

type HTTPOptions struct {
	Name          string
	Address       string
	Authorization string
	VerifyTLS     bool
	CABundle      string
	Service       string
}

func HTTP(ctx context.Context, options HTTPOptions) domain.ConnectivityTarget {
	name, address, token, verifyTLS, caBundle := options.Name, options.Address, options.Authorization, options.VerifyTLS, options.CABundle
	registry := options.Service == "registry"
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return Result(name, "unknown", "The saved service address is invalid.")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: !verifyTLS}}
	defer transport.CloseIdleConnections()
	if caBundle != "" {
		roots, _ := x509.SystemCertPool()
		if roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM([]byte(caBundle)) {
			return Result(name, "unknown", "The saved service certificate is invalid.")
		}
		transport.TLSClientConfig.RootCAs = roots
	}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return Result(name, "unknown", "The saved service address is invalid.")
	}
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	response, err := client.Do(req)
	if err != nil {
		return Result(name, "unreachable", "Could not connect to the service. Check its address, network and TLS settings.")
	}
	defer response.Body.Close()
	// An authenticated registry normally challenges anonymous clients with 401.
	if registry && (response.StatusCode == 200 || response.StatusCode == 401) && response.Header.Get("Docker-Distribution-Api-Version") == "registry/2.0" {
		return Result(name, "reachable", "")
	}
	if options.Service == "harbor" && response.StatusCode >= 200 && response.StatusCode < 300 {
		var user struct {
			Username string `json:"username"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&user) == nil && user.Username != "" {
			return Result(name, "reachable", "")
		}
		return Result(name, "unavailable", "Harbor did not return a valid account response.")
	}
	if !registry && response.StatusCode >= 200 && response.StatusCode < 300 {
		var payload struct {
			Data *[]json.RawMessage `json:"data"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload) == nil && payload.Data != nil {
			return Result(name, "reachable", "")
		}
		return Result(name, "unavailable", "The endpoint did not return a valid Proxmox response.")
	}
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return Result(name, "access-denied", "The service rejected access. Check its credentials and permissions.")
	}
	return Result(name, "unavailable", "The service responds but is not available.")
}

func Kubernetes(ctx context.Context, name, config string) domain.ConnectivityTarget {
	output, err := kubeconfig.RunKubectl(ctx, config, nil, "get", "--raw=/readyz", "--request-timeout=4s")
	return kubernetesResult(name, output, err)
}

func kubernetesResult(name string, output []byte, err error) domain.ConnectivityTarget {
	if err == nil && (strings.TrimSpace(string(output)) == "ok" || strings.HasSuffix(strings.TrimSpace(string(output)), "\nok")) {
		return Result(name, "reachable", "")
	}
	message := strings.ToLower(string(output))
	if strings.Contains(message, "forbidden") || strings.Contains(message, "unauthorized") {
		return Result(name, "access-denied", "Kubernetes rejected access. Check the saved credentials and permissions.")
	}
	if strings.Contains(message, "readyz check failed") || strings.Contains(message, "internalerror") || strings.Contains(message, "toomanyrequests") || strings.Contains(message, "too many requests") || strings.Contains(message, "overloaded") || strings.Contains(message, "serviceunavailable") || strings.Contains(message, "service unavailable") {
		return Result(name, "unavailable", "The Kubernetes API responds but is not ready.")
	}
	return Result(name, "unreachable", "Could not connect to the Kubernetes API.")
}

// An NFSv4 RPC NULL call checks the service without mounting or modifying exports
// (RFC 7530, section 15.1). Record markers follow RFC 5531, section 11.
func NFS(ctx context.Context, name, address string) domain.ConnectivityTarget {
	return nfs(ctx, name, net.JoinHostPort(address, "2049"))
}
func nfs(ctx context.Context, name, address string) domain.ConnectivityTarget {
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return Result(name, "unreachable", "Could not connect to the NFS service.")
	}
	defer conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	_ = conn.SetDeadline(deadline)
	packet := make([]byte, 44)
	for index, value := range []uint32{0x80000028, 1, 0, 2, 100003, 4, 0, 0, 0, 0, 0} {
		binary.BigEndian.PutUint32(packet[index*4:], value)
	}
	if _, err = conn.Write(packet); err != nil {
		return Result(name, "unreachable", "The NFS service did not respond.")
	}
	var marker [4]byte
	if _, err = io.ReadFull(conn, marker[:]); err != nil {
		return Result(name, "unreachable", "The NFS service did not respond.")
	}
	size := binary.BigEndian.Uint32(marker[:]) & 0x7fffffff
	if size < 24 || size > 16384 || binary.BigEndian.Uint32(marker[:])&0x80000000 == 0 {
		return Result(name, "unavailable", "The endpoint did not return a valid NFS response.")
	}
	reply := make([]byte, size)
	if _, err = io.ReadFull(conn, reply); err != nil {
		return Result(name, "unreachable", "The NFS service did not respond.")
	}
	verifierSize := binary.BigEndian.Uint32(reply[16:20])
	offset := uint64(20) + ((uint64(verifierSize) + 3) &^ uint64(3))
	if binary.BigEndian.Uint32(reply[:4]) != 1 || binary.BigEndian.Uint32(reply[4:8]) != 1 || binary.BigEndian.Uint32(reply[8:12]) != 0 || offset+4 > uint64(len(reply)) || binary.BigEndian.Uint32(reply[offset:offset+4]) != 0 {
		return Result(name, "unavailable", "NFSv4 is not available at the saved endpoint.")
	}
	return Result(name, "reachable", "")
}
