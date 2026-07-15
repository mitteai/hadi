// Package sshx is hadi's transport: a thin client over golang.org/x/crypto/ssh.
// Self-contained on purpose — no reliance on the host's ssh binary or config,
// so CI and laptops behave identically. The key comes from HADI_SSH_KEY (PEM
// contents or a path) or --ssh-key.
package sshx

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Client is an SSH connection to one box.
type Client struct {
	Host string // as dialed (IP or DNS name)
	c    *ssh.Client
}

// LoadKey resolves the private key from --ssh-key path, HADI_SSH_KEY (contents
// or path), or the default identity files.
func LoadKey(flagPath string) (ssh.Signer, error) {
	try := func(pem []byte) (ssh.Signer, error) { return ssh.ParsePrivateKey(pem) }

	if flagPath != "" {
		pem, err := os.ReadFile(expand(flagPath))
		if err != nil {
			return nil, fmt.Errorf("--ssh-key: %w", err)
		}
		return try(pem)
	}
	if v := os.Getenv("HADI_SSH_KEY"); v != "" {
		if strings.Contains(v, "PRIVATE KEY") {
			return try([]byte(v))
		}
		pem, err := os.ReadFile(expand(v))
		if err != nil {
			return nil, fmt.Errorf("HADI_SSH_KEY looks like a path but: %w", err)
		}
		return try(pem)
	}
	for _, p := range []string{"~/.ssh/id_ed25519", "~/.ssh/id_rsa"} {
		if pem, err := os.ReadFile(expand(p)); err == nil {
			if s, err := try(pem); err == nil {
				return s, nil
			}
		}
	}
	return nil, fmt.Errorf("no SSH key: set HADI_SSH_KEY, pass --ssh-key, or keep one at ~/.ssh/id_ed25519")
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return home + p[1:]
	}
	return p
}

// Dial connects as root. Host keys are not verified — the same posture as the
// bash this replaces; fixing it properly means terraform exporting host keys.
func Dial(host string, key ssh.Signer) (*Client, error) {
	cfg := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(key)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec
		Timeout:         10 * time.Second,
	}
	c, err := ssh.Dial("tcp", net.JoinHostPort(host, "22"), cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh %s: %w", host, err)
	}
	return &Client{Host: host, c: c}, nil
}

func (cl *Client) Close() error { return cl.c.Close() }

// Addr satisfies hadi's box interface.
func (cl *Client) Addr() string { return cl.Host }

// Run executes a command and returns its STDOUT. Stderr rides along only in
// the error, so callers that parse output (state files, caddy configs) never
// see diagnostic noise.
//
// Stdout and Stderr MUST be separate buffers: x/crypto/ssh silently loses
// output when both fields point at the same writer (found the hard way: every
// remote read returned "" during the first live deploys).
func (cl *Client) Run(cmd string) (string, error) {
	sess, err := cl.c.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	var so, se bytes.Buffer
	sess.Stdout = &so
	sess.Stderr = &se
	err = sess.Run(cmd)
	out := strings.TrimRight(so.String(), "\n")
	if err != nil {
		return out, fmt.Errorf("[%s] %s: %w\nstdout: %s\nstderr: %s", cl.Host, firstLine(cmd), err, out, strings.TrimSpace(se.String()))
	}
	return out, nil
}

// Push writes content to a remote path with the given mode, via a cat pipe —
// no sftp dependency, works everywhere sshd does.
func (cl *Client) Push(content []byte, path, mode string) error {
	return cl.PushReader(bytes.NewReader(content), path, mode)
}

// PushReader streams from r to a remote path — same cat pipe as Push, without
// holding the payload in memory. Image artifacts (100MB+ zstd tarballs) ship
// through here.
func (cl *Client) PushReader(r io.Reader, path, mode string) error {
	sess, err := cl.c.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	sess.Stdin = r
	var buf bytes.Buffer
	sess.Stderr = &buf
	cmd := fmt.Sprintf("mkdir -p $(dirname %q) && cat > %q && chmod %s %q", path, path, mode, path)
	if err := sess.Run(cmd); err != nil {
		return fmt.Errorf("[%s] push %s: %w\n%s", cl.Host, path, err, buf.String())
	}
	return nil
}

// Stream runs a command and streams its output line-prefixed to the writer.
// Used by logs -f; blocks until the remote command ends.
func (cl *Client) Stream(cmd, prefix string, out func(string)) error {
	sess, err := cl.c.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	pipe, err := sess.StdoutPipe()
	if err != nil {
		return err
	}
	if err := sess.Start(cmd); err != nil {
		return err
	}
	buf := make([]byte, 32*1024)
	var pending string
	for {
		n, rerr := pipe.Read(buf)
		if n > 0 {
			pending += string(buf[:n])
			for {
				i := strings.IndexByte(pending, '\n')
				if i < 0 {
					break
				}
				out(prefix + pending[:i])
				pending = pending[i+1:]
			}
		}
		if rerr != nil {
			if pending != "" {
				out(prefix + pending)
			}
			break
		}
	}
	return sess.Wait()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " ..."
	}
	return s
}
