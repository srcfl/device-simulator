package matter

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"
)

// startScriptedBridge runs a goroutine that reads NDJSON requests from r and
// writes responses (via handle) to w. It returns a function to push events.
func startScriptedBridge(r io.Reader, w io.Writer, handle func(method string, params json.RawMessage) (interface{}, string)) func(ev map[string]interface{}) {
	go func() {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			var req struct {
				ID     int64           `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
				continue
			}
			res, errStr := handle(req.Method, req.Params)
			resp := map[string]interface{}{"id": req.ID}
			if errStr != "" {
				resp["error"] = errStr
			} else {
				resp["result"] = res
			}
			b, _ := json.Marshal(resp)
			b = append(b, '\n')
			_, _ = w.Write(b)
		}
	}()
	return func(ev map[string]interface{}) {
		b, _ := json.Marshal(ev)
		b = append(b, '\n')
		_, _ = w.Write(b)
	}
}

func TestRPCRoundTrip(t *testing.T) {
	prReq, pwReq := io.Pipe()
	prResp, pwResp := io.Pipe()
	events := make(chan event, 4)

	c := newRPCConn(pwReq, prResp, func(e event) { events <- e })
	push := startScriptedBridge(prReq, pwResp, func(method string, params json.RawMessage) (interface{}, string) {
		if method == "fail" {
			return nil, "boom"
		}
		return map[string]string{"echo": method}, ""
	})

	raw, err := c.call("ping", nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	var res struct{ Echo string }
	if err := json.Unmarshal(raw, &res); err != nil || res.Echo != "ping" {
		t.Fatalf("unexpected result %s (%v)", raw, err)
	}

	if _, err := c.call("fail", nil); err == nil {
		t.Error("expected error from failing call")
	}

	push(map[string]interface{}{"event": "ready"})
	select {
	case e := <-events:
		if e.Name != "ready" {
			t.Errorf("event = %q, want ready", e.Name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

// newFakeManager builds a Manager backed by an in-process scripted bridge, with
// no real Node process.
func newFakeManager(t *testing.T, handle func(method string, params json.RawMessage) (interface{}, string)) (*Manager, func(ev map[string]interface{})) {
	t.Helper()
	prReq, pwReq := io.Pipe()
	prResp, pwResp := io.Pipe()
	m := &Manager{
		node:        NodeInfo{Available: true, Version: "vtest"},
		storageRoot: t.TempDir(),
		devices:     make(map[string]*Server),
		started:     true,
	}
	m.rpc = newRPCConn(pwReq, prResp, m.handleEvent)
	push := startScriptedBridge(prReq, pwResp, handle)
	return m, push
}

func TestServerStartAndEvents(t *testing.T) {
	m, push := newFakeManager(t, func(method string, params json.RawMessage) (interface{}, string) {
		switch method {
		case "createDevice":
			var cfg DeviceConfig
			_ = json.Unmarshal(params, &cfg)
			return map[string]interface{}{
				"nodeId": cfg.NodeID, "manualPairingCode": "34970112332",
				"qrPairingCode": "MT:TESTQR", "port": cfg.Port,
				"commissioned": false, "fabricCount": 0,
			}, ""
		case "setAttributes", "removeDevice":
			return map[string]interface{}{"ok": true}, ""
		}
		return nil, fmt.Sprintf("unknown method %s", method)
	})

	s, err := NewServer(7, "smartplug")
	if err != nil {
		t.Fatal(err)
	}
	s.mgr = m
	s.SetPort(5599)

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if s.PairingCode() != "34970112332" || s.QRCode() != "MT:TESTQR" {
		t.Errorf("pairing=%q qr=%q", s.PairingCode(), s.QRCode())
	}

	// External commissioning event should flip commissioned state.
	push(map[string]interface{}{"event": "commissioned", "nodeId": s.nodeID, "fabricCount": 1})
	waitFor(t, func() bool {
		st := s.GetState()
		return st["commissioned"] == true && st["fabric_count"] == 1
	})

	// SetValue pushes a setAttributes RPC without error.
	if err := s.SetValue("on", true); err != nil {
		t.Errorf("SetValue: %v", err)
	}

	if err := s.Stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

func TestServerStartUnavailable(t *testing.T) {
	s, err := NewServer(1, "thermostat")
	if err != nil {
		t.Fatal(err)
	}
	s.mgr = &Manager{node: NodeInfo{Available: false, Reason: "no node"}, devices: map[string]*Server{}}
	err = s.Start()
	var unavailable *ErrNodeUnavailable
	if err == nil || !errorsAs(err, &unavailable) {
		t.Fatalf("expected ErrNodeUnavailable, got %v", err)
	}
}

// errorsAs is a tiny local helper to avoid importing errors in multiple files.
func errorsAs(err error, target **ErrNodeUnavailable) bool {
	if e, ok := err.(*ErrNodeUnavailable); ok {
		*target = e
		return true
	}
	return false
}
