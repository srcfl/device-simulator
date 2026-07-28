package matter

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"
)

// readyTimeout bounds how long we wait for the bridge to announce readiness.
const readyTimeout = 20 * time.Second

// Manager owns the single Node.js bridge process that hosts every Matter
// ServerNode. Per-device matter.Server adapters delegate to it by nodeId. It is
// a process-wide singleton, started lazily when the first Matter device starts.
type Manager struct {
	mu sync.Mutex

	node        NodeInfo
	storageRoot string

	cmd     *exec.Cmd
	rpc     *rpcConn
	started bool
	readyCh chan struct{}

	devices map[string]*Server // nodeId -> Server (for event routing & replay)

	shuttingDown bool
}

var (
	sharedManager *Manager
	sharedOnce    sync.Once
)

// Shared returns the process-wide bridge manager.
func Shared() *Manager {
	sharedOnce.Do(func() {
		sharedManager = &Manager{
			node:        DetectNode(),
			storageRoot: StorageRoot(),
			devices:     make(map[string]*Server),
		}
	})
	return sharedManager
}

// Available reports whether Matter can be used and, if not, a human-readable
// reason for the UI/API to surface.
func (m *Manager) Available() (bool, string) {
	if !m.node.Available {
		return false, m.node.Reason
	}
	return true, ""
}

// NodeVersion returns the detected Node.js version (may be empty).
func (m *Manager) NodeVersion() string { return m.node.Version }

// register/unregister track live devices for event routing and crash replay.
func (m *Manager) register(s *Server) {
	m.mu.Lock()
	m.devices[s.nodeID] = s
	m.mu.Unlock()
}

func (m *Manager) unregister(nodeID string) {
	m.mu.Lock()
	delete(m.devices, nodeID)
	m.mu.Unlock()
}

// ensureStarted lazily spawns the bridge and waits for readiness.
func (m *Manager) ensureStarted() error {
	if ok, reason := m.Available(); !ok {
		return &ErrNodeUnavailable{Reason: reason}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return nil
	}
	return m.spawnLocked()
}

// spawnLocked starts the Node process. Caller holds m.mu.
func (m *Manager) spawnLocked() error {
	bridgeDir, err := ensureBridgeExtracted()
	if err != nil {
		return fmt.Errorf("prepare matter bridge: %w", err)
	}
	if err := os.MkdirAll(m.storageRoot, 0o755); err != nil {
		return fmt.Errorf("create matter storage: %w", err)
	}

	cmd := exec.Command(m.node.Path, "index.mjs")
	cmd.Dir = bridgeDir
	cmd.Env = append(os.Environ(),
		"MATTER_STORAGE_PATH="+m.storageRoot,
		"MATTER_LOG_LEVEL="+logLevel(),
	)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start matter bridge: %w", err)
	}

	m.cmd = cmd
	m.readyCh = make(chan struct{})
	m.rpc = newRPCConn(stdin, stdout, m.handleEvent)
	go m.drainStderr(stderr)
	go m.watch(cmd)

	// Wait for the bridge's "ready" event.
	ready := m.readyCh
	m.mu.Unlock()
	select {
	case <-ready:
		m.mu.Lock()
		m.started = true
		log.Printf("[matter] bridge ready (node %s)", m.node.Version)
		return nil
	case <-time.After(readyTimeout):
		m.mu.Lock()
		_ = cmd.Process.Kill()
		return fmt.Errorf("matter bridge did not become ready within %s", readyTimeout)
	}
}

// call proxies an RPC to the bridge.
func (m *Manager) call(method string, params interface{}) (json.RawMessage, error) {
	m.mu.Lock()
	rpc := m.rpc
	started := m.started
	m.mu.Unlock()
	if !started || rpc == nil {
		return nil, fmt.Errorf("matter bridge not started")
	}
	return rpc.call(method, params)
}

// createDevice creates a ServerNode for the given config and returns pairing info.
func (m *Manager) createDevice(cfg DeviceConfig) (CommissioningInfo, error) {
	if err := m.ensureStarted(); err != nil {
		return CommissioningInfo{}, err
	}
	raw, err := m.call("createDevice", cfg)
	if err != nil {
		return CommissioningInfo{}, err
	}
	var info CommissioningInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return CommissioningInfo{}, err
	}
	return info, nil
}

func (m *Manager) setAttributes(nodeID string, changes []AttrChange) error {
	_, err := m.call("setAttributes", map[string]interface{}{
		"nodeId":  nodeID,
		"changes": changes,
	})
	return err
}

func (m *Manager) removeDevice(nodeID string) error {
	_, err := m.call("removeDevice", map[string]interface{}{"nodeId": nodeID})
	return err
}

// handleEvent routes a push event from the bridge to the right Server.
func (m *Manager) handleEvent(ev event) {
	if ev.Name == "ready" {
		m.mu.Lock()
		ch := m.readyCh
		m.mu.Unlock()
		if ch != nil {
			select {
			case <-ch:
			default:
				close(ch)
			}
		}
		return
	}

	var probe struct {
		NodeID string `json:"nodeId"`
	}
	_ = json.Unmarshal(ev.Raw, &probe)
	if probe.NodeID == "" {
		return
	}
	m.mu.Lock()
	s := m.devices[probe.NodeID]
	m.mu.Unlock()
	if s != nil {
		s.handleEvent(ev)
	}
}

// drainStderr captures matter.js/bridge diagnostics and forwards them to the
// owning device's log (best-effort).
func (m *Manager) drainStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		log.Printf("[matter-bridge] %s", line)
	}
}

// watch waits for the process to exit and, unless we're shutting down, restarts
// it and replays all live devices so commissioning (persisted on disk) is
// restored.
func (m *Manager) watch(cmd *exec.Cmd) {
	err := cmd.Wait()

	m.mu.Lock()
	if m.shuttingDown || cmd != m.cmd {
		m.mu.Unlock()
		return
	}
	m.started = false
	if m.rpc != nil {
		m.rpc.markClosed()
	}
	log.Printf("[matter] bridge exited unexpectedly (%v); restarting", err)
	// mark all devices bridge-down
	devices := make([]*Server, 0, len(m.devices))
	for _, s := range m.devices {
		s.setBridgeUp(false)
		devices = append(devices, s)
	}
	m.mu.Unlock()

	backoff := time.Second
	for attempt := 0; attempt < 10; attempt++ {
		time.Sleep(backoff)
		m.mu.Lock()
		if m.shuttingDown {
			m.mu.Unlock()
			return
		}
		err := m.spawnLocked()
		m.mu.Unlock()
		if err == nil {
			break
		}
		log.Printf("[matter] bridge restart attempt %d failed: %v", attempt+1, err)
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}

	// Replay device creation against the fresh process.
	for _, s := range devices {
		if _, err := m.createDevice(s.config()); err != nil {
			log.Printf("[matter] replay of %s failed: %v", s.nodeID, err)
			continue
		}
		s.setBridgeUp(true)
	}
}

// Shutdown stops the bridge process (called on app exit).
func (m *Manager) Shutdown() {
	m.mu.Lock()
	m.shuttingDown = true
	rpc := m.rpc
	cmd := m.cmd
	m.started = false
	m.mu.Unlock()

	if rpc != nil {
		_, _ = rpc.call("shutdown", nil)
	}
	if cmd != nil && cmd.Process != nil {
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
		}
	}
}

func logLevel() string {
	if v := os.Getenv("MATTER_LOG_LEVEL"); v != "" {
		return v
	}
	return "notice"
}
