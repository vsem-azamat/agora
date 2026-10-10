package hub

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/gen/agora/v1/agorav1connect"
	"github.com/vsem-azamat/agora/internal/bridges"
)

const (
	// ProtocolVersion is the highest bridge protocol version the hub speaks, sent in hello.
	ProtocolVersion = 1
	// bridgeMinDelay and bridgeMaxDelay bound the delay before a bridge command starts again.
	bridgeMinDelay = time.Second
	bridgeMaxDelay = time.Minute
	// bridgeSteady is how long a bridge command runs before it counts as working again and its
	// restart delay goes back to bridgeMinDelay.
	bridgeSteady = time.Minute
	// bridgeStopWait is how long a bridge's process group may take to end after SIGTERM before
	// it is killed.
	bridgeStopWait = 5 * time.Second
	// maxBridgeLine bounds a line a bridge writes; longer lines are dropped.
	maxBridgeLine = 1 << 20
	// maxLogLine bounds a line of a bridge's standard error in the hub's log.
	maxLogLine = 1000
	// outBatch is how many messages the hub reads at once to hand to a bridge.
	outBatch = 100
)

// operatorProcedures are served only by the web listener; the socket refuses them as not found.
var operatorProcedures = map[string]bool{
	agorav1connect.BridgeServiceSetBridgePolicyProcedure: true,
	agorav1connect.BridgeServiceSendPendingProcedure:     true,
	agorav1connect.BridgeServiceDeclinePendingProcedure:  true,
}

// bridgeRun is a bridge the hub runs: its supervising goroutine and what it reports.
type bridgeRun struct {
	name   string
	cancel context.CancelFunc
	done   chan struct{} // closed when the supervisor returns

	mu       sync.Mutex
	state    agorav1.BridgeState
	lastExit string
	down     bool // the board said it stopped and has not said it works again
}

func (r *bridgeRun) set(state agorav1.BridgeState, lastExit string) {
	r.mu.Lock()
	r.state, r.lastExit = state, lastExit
	r.mu.Unlock()
}

// bridgeRuns holds the bridges the hub runs while it serves.
type bridgeRuns struct {
	mu     sync.Mutex
	ctx    context.Context // nil until Serve starts the bridges
	closed bool            // Serve is shutting down: nothing starts any more
	runs   map[string]*bridgeRun
	wg     sync.WaitGroup

	minDelay, maxDelay, steady time.Duration
}

// serveBridges starts every stored bridge; bridges added later start when added.
func (h *Hub) serveBridges(ctx context.Context) {
	h.bridgeRuns.mu.Lock()
	h.bridgeRuns.ctx = ctx
	h.bridgeRuns.mu.Unlock()
	list, err := h.bridges.List(ctx)
	if err != nil {
		h.log.Error("bridges: list", "err", err)
		return
	}
	for _, b := range list {
		h.startBridge(b.Name) //nolint:contextcheck // bridges run under the context serveBridges stored, which is ctx
	}
}

// stopBridges stops every bridge and waits until their process groups are gone.
func (h *Hub) stopBridges() {
	h.bridgeRuns.mu.Lock()
	h.bridgeRuns.closed = true
	for _, r := range h.bridgeRuns.runs {
		r.cancel()
	}
	h.bridgeRuns.mu.Unlock()
	h.bridgeRuns.wg.Wait()
}

func (h *Hub) startBridge(name string) {
	br := &h.bridgeRuns
	br.mu.Lock()
	defer br.mu.Unlock()
	if br.ctx == nil || br.closed || br.runs[name] != nil {
		return
	}
	ctx, cancel := context.WithCancel(br.ctx)
	r := &bridgeRun{name: name, cancel: cancel, done: make(chan struct{}), state: agorav1.BridgeState_BRIDGE_STATE_RUNNING}
	br.runs[name] = r
	br.wg.Add(1)
	go func() {
		defer br.wg.Done()
		defer close(r.done)
		h.superviseBridge(ctx, r)
	}()
}

// stopBridge stops the bridge name, if it runs, and waits for it; it reports whether it ran.
func (h *Hub) stopBridge(name string) bool {
	br := &h.bridgeRuns
	br.mu.Lock()
	r := br.runs[name]
	delete(br.runs, name)
	br.mu.Unlock()
	if r == nil {
		return false
	}
	r.cancel()
	<-r.done
	return true
}

func (h *Hub) bridgeState(name string) (agorav1.BridgeState, string) {
	br := &h.bridgeRuns
	br.mu.Lock()
	r := br.runs[name]
	br.mu.Unlock()
	if r == nil {
		return agorav1.BridgeState_BRIDGE_STATE_STOPPED, ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state, r.lastExit
}

// nextBridgeDelay doubles the delay before the next start of a bridge, up to limit.
func nextBridgeDelay(d, limit time.Duration) time.Duration { return min(2*d, limit) }

// superviseBridge runs a bridge's command until ctx ends, starting it again after it exits with
// a delay that doubles up to maxDelay and goes back to minDelay after a run of steady.
func (h *Hub) superviseBridge(ctx context.Context, r *bridgeRun) {
	br := &h.bridgeRuns
	delay := br.minDelay
	for {
		started := time.Now()
		exit := h.runBridge(ctx, r)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) >= br.steady {
			delay = br.minDelay
		}
		h.log.Warn("bridge stopped; starting it again", "bridge", r.name, "exit", exit, "delay", delay)
		r.set(agorav1.BridgeState_BRIDGE_STATE_RESTARTING, exit)
		r.mu.Lock()
		first := !r.down
		r.down = true
		r.mu.Unlock()
		if first {
			h.bridgeNotice(ctx, r.name, fmt.Sprintf("The bridge stopped (%s). The hub starts it again and says here when it works again.", exit))
		}
		h.changes.fire()
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = nextBridgeDelay(delay, br.maxDelay)
	}
}

// bridgeNotice has the board post text in the bridge's room, also while the hub stops.
func (h *Hub) bridgeNotice(ctx context.Context, name, text string) {
	if err := h.bridges.Notice(context.WithoutCancel(ctx), name, text); err != nil {
		h.log.Error("bridge notice", "bridge", name, "err", err)
	}
}

// runBridge runs the bridge's command once, in its own process group, and returns how it ended.
// When ctx ends it stops the group with SIGTERM, then SIGKILL after bridgeStopWait. It returns
// only once the command's output is read and every goroutine it started has ended.
func (h *Hub) runBridge(ctx context.Context, r *bridgeRun) string {
	b, err := h.bridges.Get(ctx, r.name)
	if err != nil {
		return err.Error()
	}
	cmd := exec.Command("sh", "-c", b.Command) //nolint:gosec // bridge commands are added by agents on the hub owner's socket, like the wake command
	cmd.Env = append(os.Environ(), "AGORA_BRIDGE="+b.Name, "AGORA_ROOM="+b.Name)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err.Error()
	}
	// Pipes of our own: Wait does not close their reading ends, so every line is read.
	outR, outW, err := os.Pipe()
	if err != nil {
		return err.Error()
	}
	defer outR.Close()
	errR, errW, err := os.Pipe()
	if err != nil {
		outW.Close()
		return err.Error()
	}
	defer errR.Close()
	cmd.Stdout, cmd.Stderr = outW, errW
	err = cmd.Start()
	outW.Close()
	errW.Close()
	if err != nil {
		return err.Error()
	}
	r.set(agorav1.BridgeState_BRIDGE_STATE_RUNNING, "")
	h.changes.fire()
	h.log.Info("bridge started", "bridge", b.Name, "pid", cmd.Process.Pid)
	pid := cmd.Process.Pid
	exited := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		_ = syscall.Kill(-pid, syscall.SIGKILL) // whatever the command left in its group
		close(exited)
	}()
	var wg sync.WaitGroup
	wg.Go(func() {
		select {
		case <-exited:
		case <-ctx.Done():
			_ = syscall.Kill(-pid, syscall.SIGTERM)
			select {
			case <-exited:
			case <-time.After(bridgeStopWait):
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}
	})
	wg.Go(func() {
		select {
		case <-exited:
		case <-time.After(h.bridgeRuns.steady):
			r.mu.Lock()
			wasDown := r.down
			r.down = false
			r.mu.Unlock()
			if wasDown {
				h.bridgeNotice(ctx, b.Name, "The bridge works again.")
				h.changes.fire()
			}
		}
	})
	wg.Go(func() {
		readLines(errR, maxLogLine, func(line []byte) {
			h.log.Warn("bridge stderr", "bridge", b.Name, "line", string(line))
		}, func() {
			h.log.Warn("bridge stderr line too long; skipped", "bridge", b.Name, "max", maxLogLine)
		})
	})
	wg.Go(func() { h.feedBridge(ctx, exited, stdin, b) })
	readLines(outR, maxBridgeLine, func(line []byte) { h.bridgeLine(ctx, b.Name, line) }, func() {
		h.log.Warn("bridge line too long; skipped", "bridge", b.Name, "max", maxBridgeLine)
	})
	<-exited
	wg.Wait()
	var ee *exec.ExitError
	switch {
	case waitErr == nil:
		return "exit status 0"
	case errors.As(waitErr, &ee):
		return ee.Error()
	}
	return waitErr.Error()
}

// readLines calls line with every line of r, without its line end; a line longer than limit
// bytes is dropped and reported to tooLong instead.
func readLines(r io.Reader, limit int, line func([]byte), tooLong func()) {
	br := bufio.NewReaderSize(r, 64<<10)
	var buf []byte
	over := false
	for {
		chunk, err := br.ReadSlice('\n')
		if !over {
			if len(buf)+len(chunk) > limit+1 { // +1: the newline
				over, buf = true, buf[:0]
			} else {
				buf = append(buf, chunk...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if over {
			tooLong()
		} else if l := bytes.TrimRight(buf, "\r\n"); len(l) > 0 {
			line(l)
		}
		buf, over = buf[:0], false
		if err != nil {
			return
		}
	}
}

// helloLine is the first line the hub writes to a bridge.
type helloLine struct {
	Type    string `json:"type"`
	Version int    `json:"version"`
	Bridge  string `json:"bridge"`
	Room    string `json:"room"`
	Cursor  string `json:"cursor"`
}

// outLine hands a message to a bridge to send.
type outLine struct {
	Type    string `json:"type"`
	ID      int64  `json:"id"`
	Author  string `json:"author"`
	Text    string `json:"text"`
	ReplyTo string `json:"reply_to,omitempty"`
}

// feedBridge writes hello and then, in posting order, every message the bridge has to send,
// until the command exits or ctx ends. A restarted bridge gets the unanswered ones again.
func (h *Hub) feedBridge(ctx context.Context, exited <-chan struct{}, w io.Writer, b bridges.Bridge) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(helloLine{Type: "hello", Version: ProtocolVersion, Bridge: b.Name, Room: b.Name, Cursor: b.Cursor}); err != nil {
		return
	}
	var last int64
	for {
		changed := h.changes.wait()
		outs, err := h.bridges.Outgoing(ctx, b.Name, last, outBatch)
		if err != nil && ctx.Err() == nil {
			h.log.Error("bridge: messages to send", "bridge", b.Name, "err", err)
		}
		for _, o := range outs {
			if err := enc.Encode(outLine{Type: "out", ID: o.ID, Author: o.Author, Text: o.Text, ReplyTo: o.ReplyTo}); err != nil {
				return
			}
			last = o.ID
		}
		if len(outs) == outBatch {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-exited:
			return
		case <-changed:
		case <-time.After(sweepEvery):
		}
	}
}

// bridgeInput is any line a bridge writes; each type uses some of the fields.
type bridgeInput struct {
	Type   string          `json:"type"`
	ID     json.RawMessage `json:"id"`
	Author struct {
		ID   json.RawMessage `json:"id"`
		Name string          `json:"name"`
		Self bool            `json:"self"`
	} `json:"author"`
	Text      string          `json:"text"`
	ReplyTo   json.RawMessage `json:"reply_to"`
	At        string          `json:"at"`
	Cursor    json.RawMessage `json:"cursor"`
	Addressed bool            `json:"addressed"`
	ExtID     json.RawMessage `json:"ext_id"`
	Error     string          `json:"error"`
}

// extString reads an identifier given as a JSON string or number; anything else is "".
func extString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

// bridgeLine handles one line a bridge wrote. Lines of unknown types and unknown fields are
// ignored.
func (h *Hub) bridgeLine(ctx context.Context, name string, raw []byte) {
	var l bridgeInput
	if err := json.Unmarshal(raw, &l); err != nil {
		h.log.Warn("bridge line is not a JSON object; skipped", "bridge", name, "err", err)
		return
	}
	ctx = context.WithoutCancel(ctx) // the line is handled even while the hub stops; Serve waits for it
	switch l.Type {
	case "in":
		in := bridges.In{
			ID: extString(l.ID), AuthorID: extString(l.Author.ID), AuthorName: l.Author.Name, Self: l.Author.Self,
			Text: l.Text, ReplyTo: extString(l.ReplyTo), Cursor: extString(l.Cursor), Addressed: l.Addressed,
		}
		if l.At != "" {
			at, err := time.Parse(time.RFC3339Nano, l.At)
			if err != nil {
				h.log.Warn("bridge in line: at is not RFC 3339; using now", "bridge", name, "at", l.At)
			}
			in.At = at
		}
		id, err := h.bridges.Receive(ctx, name, in, h.webAs)
		if err != nil {
			h.log.Warn("bridge in line; skipped", "bridge", name, "err", err)
			return
		}
		if id != 0 {
			h.changes.fire()
		}
	case "sent", "failed":
		id, err := strconv.ParseInt(extString(l.ID), 10, 64)
		if err != nil {
			h.log.Warn("bridge "+l.Type+" line without a message id; skipped", "bridge", name)
			return
		}
		var changed bool
		if l.Type == "sent" {
			changed, err = h.bridges.Sent(ctx, name, id, extString(l.ExtID))
		} else {
			changed, err = h.bridges.Failed(ctx, name, id, l.Error)
		}
		if err != nil {
			h.log.Error("bridge "+l.Type+" line", "bridge", name, "id", id, "err", err)
		}
		if changed {
			h.changes.fire()
		}
	}
}

// --- BridgeService --------------------------------------------------------------------

type bridgeService struct{ h *Hub }

var bridgePolicies = map[bridges.Policy]agorav1.BridgePolicy{
	bridges.Approve: agorav1.BridgePolicy_BRIDGE_POLICY_APPROVE,
	bridges.Open:    agorav1.BridgePolicy_BRIDGE_POLICY_OPEN,
	bridges.Read:    agorav1.BridgePolicy_BRIDGE_POLICY_READ,
}

func (s *bridgeService) AddBridge(ctx context.Context, req *connect.Request[agorav1.AddBridgeRequest]) (*connect.Response[agorav1.AddBridgeResponse], error) {
	m := req.Msg
	created, err := s.h.bridges.Add(ctx, m.GetAgent(), m.GetName(), m.GetCommand(), m.GetPurpose(), m.GetAddressees())
	if err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	s.h.startBridge(m.GetName()) //nolint:contextcheck // the bridge outlives the call: it runs under Serve's context
	return connect.NewResponse(&agorav1.AddBridgeResponse{CreatedRoom: created}), nil
}

func (s *bridgeService) RemoveBridge(ctx context.Context, req *connect.Request[agorav1.RemoveBridgeRequest]) (*connect.Response[agorav1.RemoveBridgeResponse], error) {
	name := req.Msg.GetName()
	ran := s.h.stopBridge(name) // first, so a stopping bridge does not find itself gone
	if err := s.h.bridges.Remove(ctx, req.Msg.GetAgent(), name); err != nil {
		if ran {
			s.h.startBridge(name) //nolint:contextcheck // not removed: it keeps running, under Serve's context
		}
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.RemoveBridgeResponse{}), nil
}

func (s *bridgeService) ListBridges(ctx context.Context, _ *connect.Request[agorav1.ListBridgesRequest]) (*connect.Response[agorav1.ListBridgesResponse], error) {
	list, err := s.h.bridges.List(ctx)
	if err != nil {
		return nil, toConnect(err)
	}
	out := &agorav1.ListBridgesResponse{}
	for _, b := range list {
		state, exit := s.h.bridgeState(b.Name)
		out.Bridges = append(out.Bridges, &agorav1.Bridge{
			Name: b.Name, Command: b.Command, Policy: bridgePolicies[b.Policy], CreatedBy: b.CreatedBy, CreatedAt: timestamppb.New(b.CreatedAt),
			Addressees: b.Addressees, State: state, LastExit: exit,
		})
	}
	return connect.NewResponse(out), nil
}

func (s *bridgeService) SetBridgePolicy(ctx context.Context, req *connect.Request[agorav1.SetBridgePolicyRequest]) (*connect.Response[agorav1.SetBridgePolicyResponse], error) {
	policy := bridges.Policy("")
	for name, v := range bridgePolicies {
		if v == req.Msg.GetPolicy() {
			policy = name
		}
	}
	if err := s.h.bridges.SetPolicy(ctx, req.Msg.GetName(), policy); err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.SetBridgePolicyResponse{}), nil
}

func (s *bridgeService) SendPending(ctx context.Context, req *connect.Request[agorav1.SendPendingRequest]) (*connect.Response[agorav1.SendPendingResponse], error) {
	if err := s.h.bridges.SendPending(ctx, req.Msg.GetMessageId()); err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.SendPendingResponse{}), nil
}

func (s *bridgeService) DeclinePending(ctx context.Context, req *connect.Request[agorav1.DeclinePendingRequest]) (*connect.Response[agorav1.DeclinePendingResponse], error) {
	if err := s.h.bridges.DeclinePending(ctx, req.Msg.GetMessageId(), s.h.webAs); err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.DeclinePendingResponse{}), nil
}
