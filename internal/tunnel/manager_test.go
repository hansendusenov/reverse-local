package tunnel

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestTunnelManagerCRUDAndForwarding(t *testing.T) {
	// 1. Start a local target TCP echo server representing the remote service
	targetLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for target server: %v", err)
	}
	defer targetLn.Close()
	targetPort := targetLn.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := targetLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	// 2. Start an in-memory Go SSH server supporting password auth and direct-tcpip forwarding
	sshLn, stopSSH := startMockSSHServer(t, "testuser", "secret123")
	defer stopSSH()
	sshPort := sshLn.Addr().(*net.TCPAddr).Port

	// Find a free port for local forwarding (-L)
	freeLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free local port: %v", err)
	}
	localPort := freeLn.Addr().(*net.TCPAddr).Port
	_ = freeLn.Close()

	// 3. Create Manager and add tunnel profile
	tmpDir := t.TempDir()
	mgr, err := NewManager(filepath.Join(tmpDir, "tunnels.json"))
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer mgr.StopAll()

	view, err := mgr.Add(TunnelConfig{
		Name:       "Staging Echo Service",
		SSHHost:    "127.0.0.1",
		SSHPort:    sshPort,
		SSHUser:    "testuser",
		AuthType:   "password",
		Secret:     "secret123",
		SaveSecret: true,
		LocalAddr:  "127.0.0.1",
		LocalPort:  localPort,
		RemoteHost: "127.0.0.1",
		RemotePort: targetPort,
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if !view.HasSavedSecret || view.Status != "stopped" {
		t.Fatalf("unexpected initial tunnel view: %+v", view)
	}

	// 4. Start the tunnel (-L -N)
	startedView, err := mgr.Start(view.ID, "")
	if err != nil {
		t.Fatalf("Start tunnel failed: %v", err)
	}
	if startedView.Status != "connected" {
		t.Fatalf("expected connected status, got %s (%s)", startedView.Status, startedView.LastError)
	}

	// 5. Connect to local forwarded port and verify bidirectional data transfer
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 3*time.Second)
	if err != nil {
		t.Fatalf("failed to connect to local forwarded port: %v", err)
	}
	payload := []byte("HELLO_THROUGH_SSH_TUNNEL")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	_ = conn.Close()

	if string(reply) != string(payload) {
		t.Fatalf("expected %q, got %q", string(payload), string(reply))
	}

	// Verify byte counters updated
	time.Sleep(50 * time.Millisecond)
	all := mgr.GetAll()
	if len(all) != 1 || all[0].BytesIn < int64(len(payload)) || all[0].BytesOut < int64(len(payload)) {
		t.Fatalf("expected byte counters >= %d, got %+v", len(payload), all[0])
	}

	// 6. Stop and delete
	if err := mgr.Stop(view.ID); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if err := mgr.Delete(view.ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

type directTCPIPExtraData struct {
	Host       string
	Port       uint32
	OriginHost string
	OriginPort uint32
}

func startMockSSHServer(t *testing.T, expectedUser, expectedPass string) (net.Listener, func()) {
	t.Helper()

	hostKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate SSH host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatalf("failed to create SSH signer: %v", err)
	}

	serverCfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == expectedUser && string(pass) == expectedPass {
				return nil, nil
			}
			return nil, fmt.Errorf("unauthorized")
		},
	}
	serverCfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for mock SSH server: %v", err)
	}

	go func() {
		for {
			tcpConn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				sshConn, chans, reqs, err := ssh.NewServerConn(c, serverCfg)
				if err != nil {
					return
				}
				defer sshConn.Close()

				go func() {
					for req := range reqs {
						if req.WantReply {
							_ = req.Reply(true, nil)
						}
					}
				}()

				for newChan := range chans {
					if newChan.ChannelType() != "direct-tcpip" {
						_ = newChan.Reject(ssh.UnknownChannelType, "unsupported channel")
						continue
					}
					var extra directTCPIPExtraData
					if err := ssh.Unmarshal(newChan.ExtraData(), &extra); err != nil {
						_ = newChan.Reject(ssh.Prohibited, "invalid payload")
						continue
					}
					targetAddr := net.JoinHostPort(extra.Host, strconv.Itoa(int(extra.Port)))
					targetConn, err := net.DialTimeout("tcp", targetAddr, 2*time.Second)
					if err != nil {
						_ = newChan.Reject(ssh.ConnectionFailed, err.Error())
						continue
					}
					ch, chReqs, err := newChan.Accept()
					if err != nil {
						_ = targetConn.Close()
						continue
					}
					go ssh.DiscardRequests(chReqs)
					go func() {
						defer ch.Close()
						defer targetConn.Close()
						go func() { _, _ = io.Copy(ch, targetConn) }()
						_, _ = io.Copy(targetConn, ch)
					}()
				}
			}(tcpConn)
		}
	}()

	return ln, func() { _ = ln.Close() }
}

