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

	mu      sync.Mutex
	closed  bool
	paths   []string
	auths   []string
	actions []string
	conn    *websocket.Conn
	seq     int
}

// New starts a Central System that stops with the test.
func New(t testing.TB) *CSMS {
	f := &CSMS{t: t}
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
			switch action {
			case "BootNotification":
				result = map[string]interface{}{"status": "Accepted", "currentTime": time.Now().UTC().Format(time.RFC3339), "interval": 60}
			case "StartTransaction":
				result = map[string]interface{}{"transactionId": 42, "idTagInfo": map[string]string{"status": "Accepted"}}
			}
			out, _ := json.Marshal([]interface{}{3, id, result})
			f.mu.Lock()
			f.actions = append(f.actions, action)
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

// Actions lists the charge point's CALLs in the order they arrived.
func (f *CSMS) Actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.actions...)
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
