// Package ocpptest provides a fake OCPP 1.6 Central System for tests of
// the simulated charger in client mode.
package ocpptest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// CSMS accepts every BootNotification, gives every StartTransaction
// transaction id 42, answers every other CALL with an empty result, and
// records what the charge point sent.
type CSMS struct {
	t   testing.TB
	srv *httptest.Server

	mu       sync.Mutex
	interval int // heartbeat interval in the BootNotification answer
	closed   bool
	paths    []string
	auths    []string
	actions  []string
	payloads []json.RawMessage
	conn     *websocket.Conn
	seq      int
}

// New starts a Central System that stops with the test.
func New(t testing.TB) *CSMS {
	f := &CSMS{t: t, interval: 60}
	up := websocket.Upgrader{Subprotocols: []string{"ocpp1.6"}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		if f.closed {
			f.mu.Unlock()
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		f.paths = append(f.paths, r.URL.Path)
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		f.mu.Unlock()
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conn = c
		f.mu.Unlock()
		for {
			_, b, err := c.ReadMessage()
			if err != nil {
				return
			}
			var frame []json.RawMessage
			if json.Unmarshal(b, &frame) != nil || len(frame) < 3 {
				continue
			}
			var typ int
			var id, action string
			json.Unmarshal(frame[0], &typ)
			json.Unmarshal(frame[1], &id)
			if typ != 2 {
				continue
			}
			json.Unmarshal(frame[2], &action)
			var result interface{} = map[string]interface{}{}
			f.mu.Lock()
			interval := f.interval
			f.mu.Unlock()
			switch action {
			case "BootNotification":
				result = map[string]interface{}{"status": "Accepted", "currentTime": time.Now().UTC().Format(time.RFC3339), "interval": interval}
			case "StartTransaction":
				result = map[string]interface{}{"transactionId": 42, "idTagInfo": map[string]string{"status": "Accepted"}}
			}
			out, _ := json.Marshal([]interface{}{3, id, result})
			var payload json.RawMessage
			if len(frame) > 3 {
				payload = frame[3]
			}
			f.mu.Lock()
			f.actions = append(f.actions, action)
			f.payloads = append(f.payloads, payload)
			c.WriteMessage(websocket.TextMessage, out)
			f.mu.Unlock()
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// URL is the Central System's WebSocket URL, with userinfo ("user:pass")
// when it is not empty.
func (f *CSMS) URL(userinfo string) string {
	u := strings.Replace(f.srv.URL, "http://", "ws://", 1)
	if userinfo != "" {
		u = strings.Replace(u, "ws://", "ws://"+userinfo+"@", 1)
	}
	return u + "/ocpp"
}

// SetHeartbeatInterval sets the interval the BootNotification answer gives.
func (f *CSMS) SetHeartbeatInterval(s int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.interval = s
}

// SetClosed makes the Central System refuse connections, as one that is down.
func (f *CSMS) SetClosed(closed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = closed
}

// Call sends a CALL to the connected charge point.
func (f *CSMS) Call(action string, payload interface{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	out, _ := json.Marshal([]interface{}{2, fmt.Sprintf("csms-%d", f.seq), action, payload})
	if f.conn == nil {
		f.t.Fatalf("send %s: no charge point connected", action)
	}
	if err := f.conn.WriteMessage(websocket.TextMessage, out); err != nil {
		f.t.Fatalf("send %s: %v", action, err)
	}
}

// Sent counts the CALLs of one action the charge point has sent.
func (f *CSMS) Sent(action string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, a := range f.actions {
		if a == action {
			n++
		}
	}
	return n
}

// Payloads lists the payloads of one action's CALLs, in order.
func (f *CSMS) Payloads(action string) []json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []json.RawMessage
	for i, a := range f.actions {
		if a == action {
			out = append(out, f.payloads[i])
		}
	}
	return out
}

// Actions lists the charge point's CALLs in the order they arrived.
func (f *CSMS) Actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.actions...)
}

// Connections counts the charge point's connections so far.
func (f *CSMS) Connections() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.paths)
}

// Paths lists the request path of each connection.
func (f *CSMS) Paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

// Auths lists the Authorization header of each connection.
func (f *CSMS) Auths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.auths...)
}

// WaitFor polls ok until it holds or the timeout passes.
func WaitFor(t testing.TB, what string, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
