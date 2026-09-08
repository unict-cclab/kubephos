package pluginssh

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type Target struct {
	Address string
	Port    int
	User    string
}

type Client struct {
	lock         sync.Mutex
	fingerprints map[string]string
}

func New() *Client {
	return &Client{fingerprints: map[string]string{}}
}

func (c *Client) Run(ctx context.Context, target Target, privateKey, command string) (string, error) {
	signer, err := ssh.ParsePrivateKey([]byte(privateKey))
	if err != nil {
		return "", errors.New("invalid SSH private key")
	}
	address := net.JoinHostPort(target.Address, strconv.Itoa(target.Port))
	configuration := &ssh.ClientConfig{
		User: target.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, Timeout: 20 * time.Second,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			fingerprint := ssh.FingerprintSHA256(key)
			c.lock.Lock()
			defer c.lock.Unlock()
			if known, ok := c.fingerprints[address]; ok && known != fingerprint {
				return errors.New("SSH host key changed during operation")
			}
			c.fingerprints[address] = fingerprint
			return nil
		},
	}
	connection, err := (&net.Dialer{Timeout: 20 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return "", err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(20 * time.Second))
	clientConnection, channels, requests, err := ssh.NewClientConn(connection, address, configuration)
	if err != nil {
		return "", err
	}
	_ = connection.SetDeadline(time.Time{})
	client := ssh.NewClient(clientConnection, channels, requests)
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	type result struct {
		value []byte
		err   error
	}
	completed := make(chan result, 1)
	go func() {
		value, err := session.CombinedOutput(command)
		completed <- result{value: value, err: err}
	}()
	select {
	case <-ctx.Done():
		_ = client.Close()
		return "", ctx.Err()
	case value := <-completed:
		output := strings.TrimSpace(string(value.value))
		if value.err != nil {
			if len(output) > 1024 {
				output = output[len(output)-1024:]
			}
			return output, fmt.Errorf("remote command failed: %s", output)
		}
		return output, nil
	}
}
