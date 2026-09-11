package pluginssh

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	client, err := c.connect(ctx, target, privateKey)
	if err != nil {
		return "", err
	}
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
		value, err := session.CombinedOutput(cancellableCommand(command))
		completed <- result{value: value, err: err}
	}()
	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGTERM)
		select {
		case <-completed:
		case <-time.After(2 * time.Second):
			_ = session.Signal(ssh.SIGKILL)
		}
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

type Terminal struct {
	client       *ssh.Client
	session      *ssh.Session
	input        io.WriteCloser
	output       *io.PipeReader
	outputWriter *io.PipeWriter
	closeOnce    sync.Once
}

func (c *Client) OpenTerminal(ctx context.Context, target Target, privateKey string, columns, rows int) (*Terminal, error) {
	client, err := c.connect(ctx, target, privateKey)
	if err != nil {
		return nil, err
	}
	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, err
	}
	input, err := session.StdinPipe()
	if err != nil {
		session.Close()
		client.Close()
		return nil, err
	}
	output, outputWriter := io.Pipe()
	session.Stdout = outputWriter
	session.Stderr = outputWriter
	if err := session.RequestPty("xterm-256color", rows, columns, ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}); err != nil {
		output.Close()
		outputWriter.Close()
		session.Close()
		client.Close()
		return nil, err
	}
	if err := session.Shell(); err != nil {
		output.Close()
		outputWriter.Close()
		session.Close()
		client.Close()
		return nil, err
	}
	return &Terminal{client: client, session: session, input: input, output: output, outputWriter: outputWriter}, nil
}

func (t *Terminal) Write(value []byte) (int, error) {
	return t.input.Write(value)
}

func (t *Terminal) Read(value []byte) (int, error) {
	return t.output.Read(value)
}

func (t *Terminal) Resize(columns, rows int) error {
	return t.session.WindowChange(rows, columns)
}

func (t *Terminal) Wait() error {
	err := t.session.Wait()
	_ = t.outputWriter.Close()
	_ = t.client.Close()
	return err
}

func (t *Terminal) Close() error {
	var result error
	t.closeOnce.Do(func() {
		_ = t.session.Signal(ssh.SIGHUP)
		_ = t.input.Close()
		_ = t.outputWriter.Close()
		_ = t.output.Close()
		_ = t.session.Close()
		result = t.client.Close()
	})
	return result
}

func (c *Client) connect(ctx context.Context, target Target, privateKey string) (*ssh.Client, error) {
	signer, err := ssh.ParsePrivateKey([]byte(privateKey))
	if err != nil {
		return nil, errors.New("invalid SSH private key")
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
		return nil, err
	}
	_ = connection.SetDeadline(time.Now().Add(20 * time.Second))
	clientConnection, channels, requests, err := ssh.NewClientConn(connection, address, configuration)
	if err != nil {
		connection.Close()
		return nil, err
	}
	_ = connection.SetDeadline(time.Time{})
	return ssh.NewClient(clientConnection, channels, requests), nil
}

func cancellableCommand(command string) string {
	return "trap 'trap - TERM INT HUP; /usr/bin/kill -TERM -- -$$' TERM INT HUP; ( " + command + " ) & kubephos_remote_pid=$!; wait \"$kubephos_remote_pid\""
}
