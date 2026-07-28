package matter

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// callTimeout bounds how long a single RPC waits for its response.
const callTimeout = 15 * time.Second

// event is a push message from the bridge (no id). Raw holds the full line so
// handlers can decode the type-specific fields they care about.
type event struct {
	Name string
	Raw  json.RawMessage
}

// rpcResponse carries a correlated response back to the waiting caller.
type rpcResponse struct {
	Result json.RawMessage
	Err    string
}

// rpcConn implements newline-delimited JSON-RPC over an io.Writer (requests)
// and io.Reader (responses + events). It is transport-agnostic: the manager
// wires it to a child process's stdin/stdout, while tests wire it to an
// in-process fake bridge via io.Pipe.
type rpcConn struct {
	w       io.Writer
	writeMu sync.Mutex

	idSeq int64

	mu      sync.Mutex
	pending map[int64]chan rpcResponse

	onEvent func(event)

	closeOnce sync.Once
	closed    chan struct{}
}

func newRPCConn(w io.Writer, r io.Reader, onEvent func(event)) *rpcConn {
	c := &rpcConn{
		w:       w,
		pending: make(map[int64]chan rpcResponse),
		onEvent: onEvent,
		closed:  make(chan struct{}),
	}
	go c.readLoop(r)
	return c
}

// call sends a request and blocks until the matching response, the timeout, or
// connection close.
func (c *rpcConn) call(method string, params interface{}) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.idSeq, 1)
	ch := make(chan rpcResponse, 1)

	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	msg := struct {
		ID     int64       `json:"id"`
		Method string      `json:"method"`
		Params interface{} `json:"params,omitempty"`
	}{ID: id, Method: method, Params: params}

	line, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	line = append(line, '\n')

	c.writeMu.Lock()
	_, err = c.w.Write(line)
	c.writeMu.Unlock()
	if err != nil {
		return nil, err
	}

	select {
	case resp := <-ch:
		if resp.Err != "" {
			return nil, errors.New(resp.Err)
		}
		return resp.Result, nil
	case <-time.After(callTimeout):
		return nil, errors.New("matter bridge call " + method + " timed out")
	case <-c.closed:
		return nil, errors.New("matter bridge connection closed")
	}
}

// readLoop parses each line: responses (have "id") are routed to the waiting
// caller; events (have "event") are dispatched to onEvent.
func (c *rpcConn) readLoop(r io.Reader) {
	defer c.markClosed()

	sc := bufio.NewScanner(r)
	// matter.js QR/state payloads can be large; allow generous line size.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}

		var probe struct {
			ID     *int64          `json:"id"`
			Event  string          `json:"event"`
			Result json.RawMessage `json:"result"`
			Error  string          `json:"error"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			// Not a control frame (stray output) — ignore.
			continue
		}

		if probe.Event != "" {
			if c.onEvent != nil {
				raw := make([]byte, len(line))
				copy(raw, line)
				c.onEvent(event{Name: probe.Event, Raw: raw})
			}
			continue
		}

		if probe.ID != nil {
			c.mu.Lock()
			ch := c.pending[*probe.ID]
			c.mu.Unlock()
			if ch != nil {
				ch <- rpcResponse{Result: probe.Result, Err: probe.Error}
			}
		}
	}
}

func (c *rpcConn) markClosed() {
	c.closeOnce.Do(func() { close(c.closed) })
}

func (c *rpcConn) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}
