package connectivity

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPProbesValidateServiceAndDoNotFollowCredentialRedirects(t *testing.T) {
	redirected := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true; w.WriteHeader(200) }))
	defer destination.Close()
	for _, test := range []struct {
		name          string
		status        int
		body, version string
		registry      bool
		want          string
	}{
		{"Proxmox", 200, `{"data":[]}`, "", false, "reachable"},
		{"denied", 401, `secret-server-details`, "", false, "access-denied"},
		{"HTML", 200, `<html>Sign in</html>`, "", false, "unavailable"},
		{"redirect", 302, "", "", false, "unavailable"},
		{"registry challenge", 401, "", "registry/2.0", true, "reachable"},
		{"registry login page", 200, "<html>login</html>", "", true, "unavailable"},
		{"registry unavailable", 503, "", "registry/2.0", true, "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "test-credential" {
					t.Error("missing API authorization")
				}
				w.Header().Set("Location", destination.URL)
				w.Header().Set("Docker-Distribution-Api-Version", test.version)
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			service := "proxmox"
			if test.registry {
				service = "registry"
			}
			result := HTTP(context.Background(), HTTPOptions{Name: "service", Address: server.URL, Authorization: "test-credential", VerifyTLS: true, Service: service})
			if result.Status != test.want || strings.Contains(result.Message, "secret") || strings.Contains(result.Message, "test-credential") {
				t.Fatalf("unexpected result: %+v", result)
			}
		})
	}
	if redirected {
		t.Fatal("forwarded credentials to a redirect target")
	}
}
func TestHTTPHonorsTLSAndBoundedCancellation(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"data":[]}`) }))
	defer server.Close()
	if HTTP(context.Background(), HTTPOptions{Name: "Proxmox", Address: server.URL, VerifyTLS: true, Service: "proxmox"}).Status != "unreachable" {
		t.Fatal("untrusted certificate accepted")
	}
	if HTTP(context.Background(), HTTPOptions{Name: "Proxmox", Address: server.URL, Service: "proxmox"}).Status != "reachable" {
		t.Fatal("explicit connection TLS setting ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if HTTP(ctx, HTTPOptions{Name: "Proxmox", Address: server.URL, Service: "proxmox"}).Status != "unreachable" {
		t.Fatal("cancelled probe succeeded")
	}
}
func TestNFSUsesReadOnlyRPCAndRejectsInvalidReply(t *testing.T) {
	for _, accept := range []uint32{0, 1} {
		t.Run(string(rune('0'+accept)), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				request := make([]byte, 44)
				_, err = io.ReadFull(conn, request)
				if err != nil {
					done <- err
					return
				}
				if binary.BigEndian.Uint32(request[16:20]) != 100003 || binary.BigEndian.Uint32(request[20:24]) != 4 || binary.BigEndian.Uint32(request[24:28]) != 0 {
					done <- errors.New("probe attempted a non-NULL NFS operation")
					return
				}
				reply := make([]byte, 28)
				for index, value := range []uint32{0x80000018, 1, 1, 0, 0, 0, accept} {
					binary.BigEndian.PutUint32(reply[index*4:], value)
				}
				_, err = conn.Write(reply)
				done <- err
			}()
			result := nfs(context.Background(), "NFS", listener.Addr().String())
			want := "reachable"
			if accept != 0 {
				want = "unavailable"
			}
			if result.Status != want {
				t.Fatalf("unexpected result: %+v", result)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestKubernetesDistinguishesAccessAndReadiness(t *testing.T) {
	for _, test := range []struct {
		output, want string
		err          error
	}{{"ok", "reachable", nil}, {"warning\nok", "reachable", nil}, {"Forbidden token-private", "access-denied", errors.New("exit 1")}, {"readyz check failed", "unavailable", errors.New("exit 1")}, {"TooManyRequests: API server overloaded", "unavailable", errors.New("exit 1")}, {"dial tcp secret-host", "unreachable", errors.New("exit 1")}} {
		result := kubernetesResult("cluster", []byte(test.output), test.err)
		if result.Status != test.want || strings.Contains(result.Message, "private") || strings.Contains(result.Message, "secret-host") {
			t.Fatalf("unexpected result: %+v", result)
		}
	}
}

func TestHarborChecksStoredAccountAccess(t *testing.T) {
	for _, test := range []struct {
		body string
		code int
		want string
	}{{`{"username":"admin"}`, 200, "reachable"}, {`<html>Sign in</html>`, 200, "unavailable"}, {`{"error":"private-auth-details"}`, 401, "access-denied"}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Basic dXNlcjpwYXNz" {
				t.Error("missing account authorization")
			}
			w.WriteHeader(test.code)
			_, _ = io.WriteString(w, test.body)
		}))
		result := HTTP(context.Background(), HTTPOptions{Name: "Harbor", Address: server.URL, Authorization: "Basic dXNlcjpwYXNz", VerifyTLS: true, Service: "harbor"})
		server.Close()
		if result.Status != test.want || strings.Contains(result.Message, "private-auth-details") {
			t.Fatalf("unexpected report: %+v", result)
		}
	}
}
