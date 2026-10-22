package ocpp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeCSMS is a Central System for client-mode tests. It accepts every
// BootNotification, answers every other CALL with an empty result, and
// records what the charge point sent.
type fakeCSMS struct {
	t   *testing.T
	srv *httptest.Server

	mu      sync.Mutex
	closed  bool // refuse connections, like a Central System that is down
	paths   []string
	auths   []string
	actions []string
	conn    *websocket.Conn
	seq     int
}

func newFakeCSMS(t *testing.T) *fakeCSMS {
	f := &fakeCSMS{t: t}
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
			if typ != MessageTypeCall {
				continue
			}
			json.Unmarshal(frame[2], &action)
			f.mu.Lock()
			f.actions = append(f.actions, action)
			f.mu.Unlock()
			var result interface{} = map[string]interface{}{}
			switch action {
			case ActionBootNotification:
				result = map[string]interface{}{"status": "Accepted", "currentTime": FormatTimestamp(time.Now()), "interval": 60}
			case ActionStartTransaction:
				result = map[string]interface{}{"transactionId": 42, "idTagInfo": map[string]string{"status": "Accepted"}}
			}
			out, _ := json.Marshal([]interface{}{MessageTypeCallResult, id, result})
			f.mu.Lock()
			c.WriteMessage(websocket.TextMessage, out)
			f.mu.Unlock()
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// url is the Central System's WebSocket URL with an optional user:pass.
func (f *fakeCSMS) url(userinfo string) string {
	u := strings.Replace(f.srv.URL, "http://", "ws://", 1)
	if userinfo != "" {
		u = strings.Replace(u, "ws://", "ws://"+userinfo+"@", 1)
	}
	return u + "/ocpp"
}

// call sends a CALL to the charge point.
func (f *fakeCSMS) call(action string, payload interface{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	out, _ := json.Marshal([]interface{}{MessageTypeCall, "csms-" + string(rune('0'+f.seq)), action, payload})
	if err := f.conn.WriteMessage(websocket.TextMessage, out); err != nil {
		f.t.Fatalf("send %s: %v", action, err)
	}
}

func (f *fakeCSMS) sent(action string) int {
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

func waitFor(t *testing.T, what string, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func startClient(t *testing.T, url string) *Server {
	t.Helper()
	s := newTestServer()
	s.SetOCPPURL(url)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop() })
	return s
}

// A Central System that asks for Basic auth gets it from the URL, and the
// charge point identity is still the last path segment.
func TestClientSendsBasicAuthFromURL(t *testing.T) {
	csms := newFakeCSMS(t)
	startClient(t, csms.url("ftw:s3cret"))
	waitFor(t, "BootNotification", 5*time.Second, func() bool { return csms.sent(ActionBootNotification) > 0 })

	csms.mu.Lock()
	defer csms.mu.Unlock()
	if csms.paths[0] != "/ocpp/OCPPSIM01" {
		t.Errorf("path = %q, want /ocpp/OCPPSIM01", csms.paths[0])
	}
	// "ftw:s3cret" in base64.
	if csms.auths[0] != "Basic ZnR3OnMzY3JldA==" {
		t.Errorf("Authorization = %q, want Basic auth for ftw:s3cret", csms.auths[0])
	}
}

func TestClientWithoutCredentialsSendsNoAuth(t *testing.T) {
	csms := newFakeCSMS(t)
	startClient(t, csms.url(""))
	waitFor(t, "BootNotification", 5*time.Second, func() bool { return csms.sent(ActionBootNotification) > 0 })

	csms.mu.Lock()
	defer csms.mu.Unlock()
	if csms.auths[0] != "" {
		t.Errorf("Authorization = %q, want none", csms.auths[0])
	}
}

// A car plugged in while the charger cannot reach its Central System still
// starts a session the Central System hears about once the charger is back.
func TestStartTransactionWaitsForTheConnection(t *testing.T) {
	csms := newFakeCSMS(t)
	csms.closed = true
	s := newTestServer()
	s.reconnectDelay = 50 * time.Millisecond
	s.SetOCPPURL(csms.url(""))
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop() })

	if err := s.SetValue("evConnected", true); err != nil {
		t.Fatal(err)
	}
	// The charger starts the session locally 2 s after plug-in.
	waitFor(t, "local transaction", 5*time.Second, func() bool {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return len(s.transactions) == 1
	})
	s.txQueueMu.Lock()
	queued := len(s.txQueue)
	s.txQueueMu.Unlock()
	if queued != 1 {
		t.Fatalf("%d messages queued while offline, want the StartTransaction", queued)
	}

	csms.mu.Lock()
	csms.closed = false
	csms.mu.Unlock()
	waitFor(t, "StartTransaction", 5*time.Second, func() bool { return csms.sent(ActionStartTransaction) == 1 })
	waitFor(t, "server transaction id", 5*time.Second, func() bool {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.transactions[42] != nil
	})

	csms.mu.Lock()
	defer csms.mu.Unlock()
	var boot, start int
	for i, a := range csms.actions {
		switch a {
		case ActionBootNotification:
			boot = i
		case ActionStartTransaction:
			start = i
		}
	}
	if start < boot {
		t.Errorf("StartTransaction sent before BootNotification: %v", csms.actions)
	}
}
