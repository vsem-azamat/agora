package hub_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/gen/agora/v1/agorav1connect"
	"github.com/vsem-azamat/agora/internal/hub"
)

// TestMain lets the test binary act as a fake bridge when FAKE_BRIDGE_DIR is set.
func TestMain(m *testing.M) {
	if dir := os.Getenv("FAKE_BRIDGE_DIR"); dir != "" {
		fakeBridge(dir)
		return
	}
	os.Exit(m.Run())
}

// fakeBridge is a bridge driven through files in dir: it appends every line it reads to
// received, its environment and pid to starts, writes the lines a test appends to send (once,
// across restarts), exits with code 3 when a file exit appears or at once while the number in
// crashes is above 0 (counting it down), and answers out lines by the
// content of mode: sent (the default, with the ext_id e<id>), fail, or silent.
func fakeBridge(dir string) {
	if err := os.Chdir(dir); err != nil {
		os.Exit(2)
	}
	appendLine("starts", fmt.Sprintf("%s %s %d %d", os.Getenv("AGORA_BRIDGE"), os.Getenv("AGORA_ROOM"), os.Getpid(), time.Now().UnixNano()))
	if raw, err := os.ReadFile("crashes"); err == nil { // crash at once this many more times
		if n, _ := strconv.Atoi(string(raw)); n > 0 {
			_ = os.WriteFile("crashes", []byte(strconv.Itoa(n-1)), 0o600) //nolint:gosec // a constant file name in the fake's own directory
			os.Exit(3)
		}
	}
	var mu sync.Mutex
	write := func(b []byte) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = os.Stdout.Write(b)
	}
	go func() {
		for {
			if os.Remove("exit") == nil {
				appendLine("exits", strconv.FormatInt(time.Now().UnixNano(), 10))
				os.Exit(3)
			}
			data, _ := os.ReadFile("send")
			raw, _ := os.ReadFile("offset")
			off, _ := strconv.Atoi(string(raw))
			if off < len(data) {
				if i := bytes.LastIndexByte(data[off:], '\n'); i >= 0 {
					write(data[off : off+i+1])
					_ = os.WriteFile("offset", []byte(strconv.Itoa(off+i+1)), 0o600) //nolint:gosec // a constant file name in the fake's own directory
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		appendLine("received", sc.Text())
		var l struct {
			Type string `json:"type"`
			ID   int64  `json:"id"`
		}
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.Type != "out" {
			continue
		}
		mode, _ := os.ReadFile("mode")
		switch strings.TrimSpace(string(mode)) {
		case "silent":
		case "fail":
			write(fmt.Appendf(nil, `{"type":"failed","id":%d,"error":"chat not found"}`+"\n", l.ID))
		default:
			write(fmt.Appendf(nil, `{"type":"sent","id":%d,"ext_id":"e%d"}`+"\n", l.ID, l.ID))
		}
	}
}

func appendLine(path, line string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line + "\n")
}

// fake is a fake bridge's directory, seen from a test.
type fake struct {
	t   *testing.T
	dir string
}

func newFake(t *testing.T) fake {
	t.Helper()
	return fake{t, shortDir(t)}
}

// command is the bridge command that runs the fake.
func (f fake) command() string {
	exe, err := os.Executable()
	if err != nil {
		f.t.Fatal(err)
	}
	return fmt.Sprintf("FAKE_BRIDGE_DIR='%s' exec '%s'", f.dir, exe)
}

func (f fake) lines(name string) []string {
	b, _ := os.ReadFile(filepath.Join(f.dir, name))
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// received returns the lines the fake read, decoded.
func (f fake) received() []map[string]any {
	var out []map[string]any
	for _, l := range f.lines("received") {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

// outs returns the ids of the out lines the fake read, in order.
func (f fake) outs() []int64 {
	var ids []int64
	for _, m := range f.received() {
		if m["type"] == "out" {
			ids = append(ids, int64(m["id"].(float64)))
		}
	}
	return ids
}

func (f fake) starts() int {
	if l := f.lines("starts"); l[0] != "" {
		return len(l)
	}
	return 0
}

// pid returns the pid of the fake's start i, counting from 0.
func (f fake) pid(i int) int {
	pid, _ := strconv.Atoi(strings.Fields(f.lines("starts")[i])[2])
	return pid
}

// times returns the times in the fake's file name: of its starts or of its exits.
func (f fake) times(name string) []time.Time {
	var out []time.Time
	for _, l := range f.lines(name) {
		fields := strings.Fields(l)
		if len(fields) == 0 {
			continue
		}
		ns, _ := strconv.ParseInt(fields[len(fields)-1], 10, 64)
		out = append(out, time.Unix(0, ns))
	}
	return out
}

func (f fake) send(lines ...string) {
	for _, l := range lines {
		appendLine(filepath.Join(f.dir, "send"), l)
	}
}

func (f fake) set(file, content string) {
	if err := os.WriteFile(filepath.Join(f.dir, file), []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// bridgeHub is a running hub that serves the web app as owner, with fast bridge restarts, and
// where secretary and builder joined.
type bridgeHub struct {
	*webHub
	hub        *hub.Hub
	bridges    agorav1connect.BridgeServiceClient // over the socket
	webBridges agorav1connect.BridgeServiceClient // over the web listener, with the token
	webRooms   agorav1connect.RoomServiceClient
}

func startBridgeHub(t *testing.T, steady time.Duration) *bridgeHub {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var h *hub.Hub
	r := startWith(t, func(hh *hub.Hub) {
		h = hh
		hub.SetBridgeDelays(h, 50*time.Millisecond, 200*time.Millisecond, steady)
		hub.SetBridgeDrain(h, 200*time.Millisecond)
		if err := h.EnableWeb(context.Background(), l, "owner"); err != nil {
			t.Fatal(err)
		}
	})
	w := &webHub{running: r, base: "http://" + l.Addr().String()}
	w.web = agorav1connect.NewWebServiceClient(r.httpClient, "http://agora")
	token := w.token(t, false)
	b := &bridgeHub{
		hub:        h,
		webHub:     w,
		bridges:    agorav1connect.NewBridgeServiceClient(r.httpClient, "http://agora"),
		webBridges: agorav1connect.NewBridgeServiceClient(w.client(token), w.base),
		webRooms:   agorav1connect.NewRoomServiceClient(w.client(token), w.base),
	}
	for _, n := range []string{"secretary", "builder"} {
		if _, err := r.sessions.JoinName(context.Background(), connect.NewRequest(&agorav1.JoinNameRequest{Name: n})); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

func (b *bridgeHub) add(t *testing.T, f fake) {
	t.Helper()
	if _, err := b.bridges.AddBridge(context.Background(), connect.NewRequest(&agorav1.AddBridgeRequest{
		Agent: "builder", Name: "example-chat", Command: f.command(), Addressees: []string{"secretary"},
	})); err != nil {
		t.Fatal(err)
	}
	eventually(t, "hello", func() bool { return len(f.received()) > 0 })
}

func (b *bridgeHub) history(t *testing.T) []*agorav1.Message {
	t.Helper()
	resp, err := b.rooms.History(context.Background(), connect.NewRequest(&agorav1.HistoryRequest{Room: "example-chat", Last: 100}))
	if err != nil {
		t.Fatal(err)
	}
	return resp.Msg.GetMessages()
}

func (b *bridgeHub) message(t *testing.T, id int64) *agorav1.Message {
	t.Helper()
	for _, m := range b.history(t) {
		if m.GetId() == id {
			return m
		}
	}
	t.Fatalf("no message %d", id)
	return nil
}

func (b *bridgeHub) post(t *testing.T, author, body string) int64 {
	t.Helper()
	resp, err := b.rooms.Post(context.Background(), connect.NewRequest(&agorav1.PostRequest{Agent: author, Room: "example-chat", Body: body}))
	if err != nil {
		t.Fatal(err)
	}
	return resp.Msg.GetId()
}

func (b *bridgeHub) setPolicy(t *testing.T, p agorav1.BridgePolicy) {
	t.Helper()
	if _, err := b.webBridges.SetBridgePolicy(context.Background(), connect.NewRequest(&agorav1.SetBridgePolicyRequest{Name: "example-chat", Policy: p})); err != nil {
		t.Fatal(err)
	}
}

func (b *bridgeHub) state(t *testing.T) *agorav1.Bridge {
	t.Helper()
	resp, err := b.bridges.ListBridges(context.Background(), connect.NewRequest(&agorav1.ListBridgesRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Msg.GetBridges()) != 1 {
		t.Fatalf("bridges: %v", resp.Msg.GetBridges())
	}
	return resp.Msg.GetBridges()[0]
}

func TestTheHubRunsABridge(t *testing.T) {
	b := startBridgeHub(t, time.Hour)
	f := newFake(t)
	b.add(t, f)
	hello := f.received()[0]
	if hello["type"] != "hello" || hello["version"] != float64(hub.ProtocolVersion) || hello["bridge"] != "example-chat" || hello["room"] != "example-chat" || hello["cursor"] != "" {
		t.Fatalf("hello: %v", hello)
	}
	if got := f.lines("starts")[0]; !strings.HasPrefix(got, "example-chat example-chat ") {
		t.Fatalf("environment: %q", got)
	}
	st := b.state(t)
	if st.GetState() != agorav1.BridgeState_BRIDGE_STATE_RUNNING || st.GetPolicy() != agorav1.BridgePolicy_BRIDGE_POLICY_APPROVE ||
		st.GetCreatedBy() != "builder" || len(st.GetAddressees()) != 1 || st.GetAddressees()[0] != "secretary" {
		t.Fatalf("bridge: %v", st)
	}
	subs, err := b.rooms.ListSubscriptions(context.Background(), connect.NewRequest(&agorav1.ListSubscriptionsRequest{Agent: "secretary"}))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range subs.Msg.GetSubscriptions() {
		found = found || (s.GetRoom() == "example-chat" && s.GetMode() == agorav1.SubscriptionMode_SUBSCRIPTION_MODE_WAKE)
	}
	if !found {
		t.Fatalf("secretary follows %v", subs.Msg.GetSubscriptions())
	}
}

func TestLinesFromABridge(t *testing.T) {
	b := startBridgeHub(t, time.Hour)
	f := newFake(t)
	b.add(t, f)
	in := `{"type":"in","id":"5513","author":{"id":"42","name":"Ada"},"text":"can you look?","mood":"curious","cursor":"5513"}`
	f.send(`{"type":"typing"}`, `not json`, in, in, `{"type":"in","id":"5514","author":{"id":42,"name":"Ada"},"text":"@builder too","reply_to":"5513","at":"2026-10-10T18:02:11Z"}`)
	eventually(t, "two messages", func() bool { return len(b.history(t)) == 2 })
	time.Sleep(100 * time.Millisecond) // nothing more arrives
	h := b.history(t)
	if len(h) != 2 {
		t.Fatalf("history: %v", h)
	}
	ext := h[0].GetExternalAuthor()
	if h[0].GetAuthor() != "" || ext.GetBridge() != "example-chat" || ext.GetId() != "42" || ext.GetName() != "Ada" || h[0].GetBody() != "can you look?" {
		t.Fatalf("first: %v", h[0])
	}
	if h[1].GetReplyTo() != h[0].GetId() || h[1].GetExternalAuthor().GetId() != "42" || !h[1].GetAt().AsTime().Equal(time.Date(2026, 10, 10, 18, 2, 11, 0, time.UTC)) {
		t.Fatalf("second: %v", h[1])
	}
	unread, err := b.rooms.Unread(context.Background(), connect.NewRequest(&agorav1.UnreadRequest{Agent: "builder", Peek: true, MentionsOnly: true}))
	if err != nil || len(unread.Msg.GetMessages()) != 1 || unread.Msg.GetMessages()[0].GetId() != h[1].GetId() {
		t.Fatalf("builder's mentions: %v %v", unread, err)
	}
	if b.state(t).GetState() != agorav1.BridgeState_BRIDGE_STATE_RUNNING || f.starts() != 1 {
		t.Fatal("the bridge did not keep running")
	}
}

func TestAddressedFromOutsideWakesTheBridgesAgent(t *testing.T) {
	b := startBridgeHub(t, time.Hour)
	f := newFake(t)
	b.add(t, f)
	b.idleAgentWithPID(t, "secretary", "session-1", "", int32(os.Getpid()))
	if _, err := b.rooms.Subscribe(context.Background(), connect.NewRequest(&agorav1.SubscribeRequest{
		Agent: "secretary", Rooms: []string{"example-chat"}, Follow: true, Mode: agorav1.SubscriptionMode_SUBSCRIPTION_MODE_MENTIONS,
	})); err != nil {
		t.Fatal(err)
	}
	woke := b.wake(t, "session-1")
	f.send(`{"type":"in","id":"1","author":{"id":"42","name":"Ada"},"text":"just chatting"}`)
	if text, ended := within(t, woke, 500*time.Millisecond); ended {
		t.Fatalf("woken by chatter: %q", text)
	}
	f.send(`{"type":"in","id":"2","author":{"id":"42","name":"Ada"},"text":"can you look at the contract?","addressed":true}`)
	text, ended := within(t, woke, 3*time.Second)
	if !ended || !strings.Contains(text, "can you look at the contract?") || !strings.Contains(text, "Ada@example-chat") {
		t.Fatalf("ended %v text %q", ended, text)
	}
}

func TestApprovedMessagesGoOutAndEchoesAreIgnored(t *testing.T) {
	b := startBridgeHub(t, time.Hour)
	f := newFake(t)
	b.add(t, f)
	f.send(`{"type":"in","id":"5513","author":{"id":"42","name":"Ada"},"text":"can you look?"}`)
	eventually(t, "the message from outside", func() bool { return len(b.history(t)) == 1 })
	resp, err := b.rooms.Post(context.Background(), connect.NewRequest(&agorav1.PostRequest{Agent: "secretary", Room: "example-chat", Body: "looked, all fine", ReplyTo: b.history(t)[0].GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	id := resp.Msg.GetId()
	time.Sleep(300 * time.Millisecond)
	if m := b.message(t, id); m.GetDeliveryState() != agorav1.DeliveryState_DELIVERY_STATE_PENDING || len(f.outs()) != 0 {
		t.Fatalf("before the operator sends: %v, outs %v", m, f.outs())
	}
	// the socket cannot send it, whatever name it acts as
	_, err = b.bridges.SendPending(context.Background(), connect.NewRequest(&agorav1.SendPendingRequest{MessageId: id}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("send over the socket: %v", err)
	}
	if _, err := b.webBridges.SendPending(context.Background(), connect.NewRequest(&agorav1.SendPendingRequest{MessageId: id})); err != nil {
		t.Fatal(err)
	}
	eventually(t, "sent", func() bool { return b.message(t, id).GetDeliveryState() == agorav1.DeliveryState_DELIVERY_STATE_SENT })
	var out map[string]any
	for _, m := range f.received() {
		if m["type"] == "out" {
			out = m
		}
	}
	if out["author"] != "secretary" || out["text"] != "looked, all fine" || out["reply_to"] != "5513" {
		t.Fatalf("out line: %v", out)
	}
	n := len(b.history(t))
	f.send(fmt.Sprintf(`{"type":"in","id":"e%d","author":{"id":"7","name":"Owner","self":true},"text":"looked, all fine"}`, id))
	time.Sleep(300 * time.Millisecond)
	if len(b.history(t)) != n {
		t.Fatal("the echo of a sent message was stored")
	}
}

func TestDeclinedMessagesNeverGoOut(t *testing.T) {
	b := startBridgeHub(t, time.Hour)
	f := newFake(t)
	b.add(t, f)
	id := b.post(t, "secretary", "something rude")
	if _, err := b.webBridges.DeclinePending(context.Background(), connect.NewRequest(&agorav1.DeclinePendingRequest{MessageId: id})); err != nil {
		t.Fatal(err)
	}
	if _, err := b.webBridges.SendPending(context.Background(), connect.NewRequest(&agorav1.SendPendingRequest{MessageId: id})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("sending a declined message: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if len(f.outs()) != 0 {
		t.Fatalf("outs: %v", f.outs())
	}
	h := b.history(t)
	if h[0].GetDeliveryState() != agorav1.DeliveryState_DELIVERY_STATE_DECLINED || h[1].GetAuthor() != "agora" ||
		h[1].GetBody() != fmt.Sprintf("@secretary your message %d was not sent: owner declined it.", id) || h[1].GetDeliveryState() != agorav1.DeliveryState_DELIVERY_STATE_UNSPECIFIED {
		t.Fatalf("history: %v", h)
	}
}

func TestRoomsCountPendingMessages(t *testing.T) {
	b := startBridgeHub(t, time.Hour)
	f := newFake(t)
	b.add(t, f)
	first := b.post(t, "secretary", "first draft")
	b.post(t, "secretary", "second draft")
	pending := func() int32 {
		resp, err := b.webRooms.ListRooms(context.Background(), connect.NewRequest(&agorav1.ListRoomsRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range resp.Msg.GetRooms() {
			if r.GetName() == "example-chat" {
				return r.GetPending()
			}
			if r.GetPending() != 0 {
				t.Fatalf("#%s counts %d pending", r.GetName(), r.GetPending())
			}
		}
		t.Fatal("no #example-chat")
		return 0
	}
	if n := pending(); n != 2 {
		t.Fatalf("pending: %d, want 2", n)
	}
	if _, err := b.webBridges.DeclinePending(context.Background(), connect.NewRequest(&agorav1.DeclinePendingRequest{MessageId: first})); err != nil {
		t.Fatal(err)
	}
	if n := pending(); n != 1 {
		t.Fatalf("pending after declining one: %d, want 1", n)
	}
}

func TestSendingWhileReadOnlySaysWhy(t *testing.T) {
	b := startBridgeHub(t, time.Hour)
	f := newFake(t)
	b.add(t, f)
	id := b.post(t, "secretary", "a draft")
	b.setPolicy(t, agorav1.BridgePolicy_BRIDGE_POLICY_READ)
	_, err := b.webBridges.SendPending(context.Background(), connect.NewRequest(&agorav1.SendPendingRequest{MessageId: id}))
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Code() != connect.CodeFailedPrecondition || cerr.Message() != "#example-chat is read-only; nothing goes out" {
		t.Fatalf("sending under read: %v", err)
	}
	if b.message(t, id).GetDeliveryState() != agorav1.DeliveryState_DELIVERY_STATE_PENDING {
		t.Fatal("the message no longer waits")
	}
	_, err = b.webBridges.SendPending(context.Background(), connect.NewRequest(&agorav1.SendPendingRequest{MessageId: id + 100}))
	if !errors.As(err, &cerr) || cerr.Code() != connect.CodeNotFound || cerr.Message() != fmt.Sprintf("message %d is not pending", id+100) {
		t.Fatalf("sending a message that does not wait: %v", err)
	}
}

func TestPoliciesOpenAndRead(t *testing.T) {
	b := startBridgeHub(t, time.Hour)
	f := newFake(t)
	b.add(t, f)
	// the operator's own message goes out under approve
	resp, err := b.webRooms.Post(context.Background(), connect.NewRequest(&agorav1.PostRequest{Room: "example-chat", Body: "hello from the app"}))
	if err != nil {
		t.Fatal(err)
	}
	fromApp := resp.Msg.GetId()
	// as owner, but over the socket: an agent's message
	asOwner := b.post(t, "owner", "pretending")
	b.setPolicy(t, agorav1.BridgePolicy_BRIDGE_POLICY_OPEN)
	open := b.post(t, "secretary", "open now")
	eventually(t, "two out lines", func() bool { return len(f.outs()) == 2 })
	if outs := f.outs(); outs[0] != fromApp || outs[1] != open {
		t.Fatalf("outs %v, want %d %d", outs, fromApp, open)
	}
	if b.message(t, asOwner).GetDeliveryState() != agorav1.DeliveryState_DELIVERY_STATE_PENDING {
		t.Fatal("a socket post as the operator went out")
	}
	b.setPolicy(t, agorav1.BridgePolicy_BRIDGE_POLICY_READ)
	_, err = b.rooms.Post(context.Background(), connect.NewRequest(&agorav1.PostRequest{Agent: "secretary", Room: "example-chat", Body: "read only"}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("post under read: %v", err)
	}
	// changing the policy is the operator's
	_, err = b.bridges.SetBridgePolicy(context.Background(), connect.NewRequest(&agorav1.SetBridgePolicyRequest{Name: "example-chat", Policy: agorav1.BridgePolicy_BRIDGE_POLICY_OPEN}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("policy over the socket: %v", err)
	}
	if b.state(t).GetPolicy() != agorav1.BridgePolicy_BRIDGE_POLICY_READ {
		t.Fatal("the socket changed the policy")
	}
}

func TestFailedMessagesTellTheAuthor(t *testing.T) {
	b := startBridgeHub(t, time.Hour)
	f := newFake(t)
	f.set("mode", "fail")
	b.add(t, f)
	b.setPolicy(t, agorav1.BridgePolicy_BRIDGE_POLICY_OPEN)
	id := b.post(t, "secretary", "hello")
	eventually(t, "failed", func() bool { return b.message(t, id).GetDeliveryState() == agorav1.DeliveryState_DELIVERY_STATE_FAILED })
	if m := b.message(t, id); m.GetDeliveryError() != "chat not found" {
		t.Fatalf("message: %v", m)
	}
	eventually(t, "the board telling secretary", func() bool {
		h := b.history(t)
		return h[len(h)-1].GetBody() == fmt.Sprintf("@secretary your message %d was not sent: chat not found.", id)
	})
}

func TestABridgeIsRestartedAndResumes(t *testing.T) {
	b := startBridgeHub(t, time.Hour)
	f := newFake(t)
	f.set("mode", "silent")
	b.add(t, f)
	b.setPolicy(t, agorav1.BridgePolicy_BRIDGE_POLICY_OPEN)
	f.send(`{"type":"in","id":"5513","author":{"id":"42","name":"Ada"},"text":"hi","cursor":"5513"}`)
	eventually(t, "the message from outside", func() bool { return len(b.history(t)) == 1 })
	id := b.post(t, "secretary", "handed out, never answered")
	eventually(t, "the out line", func() bool { return len(f.outs()) == 1 })
	f.set("exit", "")
	eventually(t, "a restart", func() bool { return f.starts() == 2 && len(f.outs()) == 2 })
	if outs := f.outs(); outs[0] != id || outs[1] != id {
		t.Fatalf("outs %v", outs)
	}
	var hellos []map[string]any
	for _, m := range f.received() {
		if m["type"] == "hello" {
			hellos = append(hellos, m)
		}
	}
	if len(hellos) != 2 || hellos[1]["cursor"] != "5513" {
		t.Fatalf("hellos: %v", hellos)
	}
	f.set("exit", "")
	eventually(t, "a third start", func() bool { return f.starts() == 3 })
	stopped := 0
	for _, m := range b.history(t) {
		if m.GetAuthor() == "agora" && strings.Contains(m.GetBody(), "The bridge stopped (exit status 3)") {
			stopped++
		}
	}
	if stopped != 1 {
		t.Fatalf("the board said %d times that the bridge stopped", stopped)
	}
}

func TestTheBoardSaysWhenABridgeWorksAgain(t *testing.T) {
	b := startBridgeHub(t, 300*time.Millisecond)
	f := newFake(t)
	b.add(t, f)
	f.set("exit", "")
	eventually(t, "works again", func() bool {
		h := b.history(t)
		return len(h) == 2 && strings.Contains(h[0].GetBody(), "stopped") && h[1].GetBody() == "The bridge works again."
	})
}

func TestRestartDelays(t *testing.T) {
	var got []time.Duration
	for d := hub.BridgeMinDelay; len(got) < 8; d = hub.NextBridgeDelay(d) {
		got = append(got, d)
	}
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60}
	for i := range want {
		if got[i] != want[i]*time.Second {
			t.Fatalf("delays %v", got)
		}
	}
	if hub.BridgeSteady != time.Minute {
		t.Fatalf("steady after %v", hub.BridgeSteady)
	}
}

func TestShutdownStopsBridges(t *testing.T) {
	b := startBridgeHub(t, time.Hour)
	f := newFake(t)
	b.add(t, f)
	pid := f.pid(0)
	b.stop()
	<-b.done
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("bridge process %d after shutdown: %v", pid, err)
	}
}

func TestRemovingABridge(t *testing.T) {
	b := startBridgeHub(t, time.Hour)
	f := newFake(t)
	b.add(t, f)
	f.send(`{"type":"in","id":"1","author":{"id":"42","name":"Ada"},"text":"hi"}`)
	eventually(t, "the message", func() bool { return len(b.history(t)) == 1 })
	// not over the web
	if _, err := b.webBridges.RemoveBridge(context.Background(), connect.NewRequest(&agorav1.RemoveBridgeRequest{Name: "example-chat"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("remove over the web: %v", err)
	}
	if b.state(t).GetState() != agorav1.BridgeState_BRIDGE_STATE_RUNNING {
		t.Fatal("the bridge stopped")
	}
	pid := f.pid(0)
	if _, err := b.bridges.RemoveBridge(context.Background(), connect.NewRequest(&agorav1.RemoveBridgeRequest{Agent: "builder", Name: "example-chat"})); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("bridge process after removing: %v", err)
	}
	h := b.history(t)
	if len(h) != 2 || h[0].GetBody() != "hi" || !strings.Contains(h[1].GetBody(), "no longer bridged") {
		t.Fatalf("history: %v", h)
	}
	resp, err := b.bridges.ListBridges(context.Background(), connect.NewRequest(&agorav1.ListBridgesRequest{}))
	if err != nil || len(resp.Msg.GetBridges()) != 0 {
		t.Fatalf("bridges: %v %v", resp, err)
	}
}

func TestTheSocketRefusesTheOperatorsCalls(t *testing.T) {
	b := startBridgeHub(t, time.Hour)
	for _, p := range []string{
		agorav1connect.BridgeServiceSetBridgePolicyProcedure, agorav1connect.BridgeServiceSendPendingProcedure, agorav1connect.BridgeServiceDeclinePendingProcedure,
		"//agora.v1.BridgeService/SendPending", "/agora.v1.BridgeService/./SendPending", "/agora.v1.BridgeService/x/../SendPending",
	} {
		req, _ := http.NewRequest(http.MethodPost, "http://agora"+p, strings.NewReader(`{"messageId":"1"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := b.httpClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound || !strings.Contains(string(body), `"code":"not_found"`) {
			t.Errorf("%s over the socket: %d %s", p, resp.StatusCode, body)
		}
	}
}

func TestAMessageSentAfterANewerOneGoesOut(t *testing.T) {
	b := startBridgeHub(t, time.Hour)
	f := newFake(t)
	f.set("mode", "silent")
	b.add(t, f)
	older := b.post(t, "secretary", "older, waits")
	newer, err := b.webRooms.Post(context.Background(), connect.NewRequest(&agorav1.PostRequest{Room: "example-chat", Body: "newer, from the app"}))
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the operator's message", func() bool { return len(f.outs()) == 1 })
	send := func(id int64) {
		if _, err := b.webBridges.SendPending(context.Background(), connect.NewRequest(&agorav1.SendPendingRequest{MessageId: id})); err != nil {
			t.Fatal(err)
		}
	}
	send(older)
	eventually(t, "the older message", func() bool { return len(f.outs()) == 2 })
	first := b.post(t, "secretary", "first pending")
	second := b.post(t, "secretary", "second pending")
	send(second)
	eventually(t, "the second pending message", func() bool { return len(f.outs()) == 3 })
	send(first)
	eventually(t, "the first pending message", func() bool { return len(f.outs()) == 4 })
	time.Sleep(200 * time.Millisecond)
	want := []int64{newer.Msg.GetId(), older, second, first}
	if got := f.outs(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("outs %v, want %v", got, want)
	}
}

func TestOutputLeftOpenByAnEscapedProcess(t *testing.T) {
	b := startBridgeHub(t, time.Hour)
	dir := shortDir(t)
	cmd := fmt.Sprintf("echo started >> '%s/starts'; setsid sleep 5 & sleep 0.2; exit 3", dir) // the pause lets setsid run before the group is killed
	if _, err := b.bridges.AddBridge(context.Background(), connect.NewRequest(&agorav1.AddBridgeRequest{Agent: "builder", Name: "example-chat", Command: cmd})); err != nil {
		t.Fatal(err)
	}
	f := fake{t, dir}
	eventually(t, "a restart", func() bool { return f.starts() >= 2 })
	done := make(chan error, 1)
	go func() {
		_, err := b.bridges.RemoveBridge(context.Background(), connect.NewRequest(&agorav1.RemoveBridgeRequest{Agent: "builder", Name: "example-chat"}))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("removing the bridge hangs")
	}
}

func TestConcurrentRemovesLeaveNothingRunning(t *testing.T) {
	b := startBridgeHub(t, time.Hour)
	f := newFake(t)
	b.add(t, f)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			_, _ = b.bridges.RemoveBridge(context.Background(), connect.NewRequest(&agorav1.RemoveBridgeRequest{Agent: "builder", Name: "example-chat"}))
		})
	}
	wg.Wait()
	time.Sleep(300 * time.Millisecond)
	if hub.RunsBridge(b.hub, "example-chat") || f.starts() != 1 {
		t.Fatalf("after removing: running %v, starts %d", hub.RunsBridge(b.hub, "example-chat"), f.starts())
	}
}

func TestASupervisorWhoseBridgeIsGoneStops(t *testing.T) {
	b := startBridgeHub(t, time.Hour)
	f := newFake(t)
	b.add(t, f)
	if _, err := b.db.Exec(`DELETE FROM bridges WHERE name = 'example-chat'`); err != nil {
		t.Fatal(err)
	}
	f.set("exit", "")
	eventually(t, "the supervisor to stop", func() bool { return !hub.RunsBridge(b.hub, "example-chat") })
	time.Sleep(200 * time.Millisecond)
	if f.starts() != 1 {
		t.Fatalf("starts %d", f.starts())
	}
	for _, m := range b.history(t) {
		if strings.Contains(m.GetBody(), "stopped") {
			t.Fatalf("notice for a bridge that is gone: %v", m)
		}
	}
}

func TestTheWebAppListsBridgesWithoutCommands(t *testing.T) {
	b := startBridgeHub(t, time.Hour)
	f := newFake(t)
	b.add(t, f)
	resp, err := b.webBridges.ListBridges(context.Background(), connect.NewRequest(&agorav1.ListBridgesRequest{}))
	if err != nil || len(resp.Msg.GetBridges()) != 1 {
		t.Fatalf("list: %v %v", resp, err)
	}
	if got := resp.Msg.GetBridges()[0]; got.GetCommand() != "" || got.GetState() != agorav1.BridgeState_BRIDGE_STATE_RUNNING || got.GetPolicy() != agorav1.BridgePolicy_BRIDGE_POLICY_APPROVE {
		t.Fatalf("bridge over the web: %v", got)
	}
	if b.state(t).GetCommand() != f.command() {
		t.Fatal("the socket lost the command")
	}
}

func TestTheRestartDelayResetsAfterASteadyRun(t *testing.T) {
	var h *hub.Hub
	r := startWith(t, func(hh *hub.Hub) {
		h = hh
		hub.SetBridgeDelays(h, 50*time.Millisecond, 5*time.Second, 400*time.Millisecond)
	})
	if _, err := r.sessions.JoinName(context.Background(), connect.NewRequest(&agorav1.JoinNameRequest{Name: "builder"})); err != nil {
		t.Fatal(err)
	}
	f := newFake(t)
	f.set("crashes", "4")
	bridges := agorav1connect.NewBridgeServiceClient(r.httpClient, "http://agora")
	if _, err := bridges.AddBridge(context.Background(), connect.NewRequest(&agorav1.AddBridgeRequest{Agent: "builder", Name: "example-chat", Command: f.command()})); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the fifth start", func() bool { return f.starts() == 5 })
	starts := f.times("starts")
	for i, want := range []time.Duration{50, 100, 200, 400} {
		if gap := starts[i+1].Sub(starts[i]); gap < want*time.Millisecond {
			t.Fatalf("delay %d: %v, want at least %vms", i, gap, want)
		}
	}
	time.Sleep(600 * time.Millisecond) // a steady run
	f.set("exit", "")
	eventually(t, "the sixth start", func() bool { return f.starts() == 6 })
	if gap := f.times("starts")[5].Sub(f.times("exits")[0]); gap > 500*time.Millisecond {
		t.Fatalf("delay after a steady run: %v; it did not go back to the first delay", gap)
	}
}

func TestACrashWakesNoOne(t *testing.T) {
	b := startBridgeHub(t, time.Hour)
	f := newFake(t)
	b.add(t, f) // secretary follows #example-chat with the mode wake
	b.idleAgentWithPID(t, "secretary", "session-1", "", int32(os.Getpid()))
	woke := b.wake(t, "session-1")
	f.set("exit", "")
	eventually(t, "the notice", func() bool { return len(b.history(t)) == 1 })
	if text, ended := within(t, woke, time.Second); ended {
		t.Fatalf("woken by the notice: %q", text)
	}
	unread, err := b.rooms.Unread(context.Background(), connect.NewRequest(&agorav1.UnreadRequest{Agent: "secretary", Peek: true}))
	if err != nil || len(unread.Msg.GetMessages()) != 1 || !strings.Contains(unread.Msg.GetMessages()[0].GetBody(), "The bridge stopped") {
		t.Fatalf("unread: %v %v", unread, err)
	}
}

func TestOutputWrittenJustBeforeTheExitIsRead(t *testing.T) {
	b := startBridgeHub(t, time.Hour)
	dir := shortDir(t)
	text := strings.Repeat("x", 200)
	// once: 1000 lines (about 270 KB, far beyond a pipe's buffer), then exit; later runs only wait
	cmd := fmt.Sprintf(`[ -e '%[1]s/done' ] && exec sleep 600; touch '%[1]s/done'
i=0; while [ $i -lt 1000 ]; do i=$((i+1)); echo '{"type":"in","id":"'$i'","author":{"name":"Ada"},"text":"%[2]s"}'; done`, dir, text)
	hub.SetBridgeDrain(b.hub, time.Millisecond) // storing the lines takes far longer: only an idle pipe may end the reading
	if _, err := b.bridges.AddBridge(context.Background(), connect.NewRequest(&agorav1.AddBridgeRequest{Agent: "builder", Name: "example-chat", Command: cmd})); err != nil {
		t.Fatal(err)
	}
	count := func() int { // messages from outside; the board's notices are not counted
		resp, err := b.rooms.History(context.Background(), connect.NewRequest(&agorav1.HistoryRequest{Room: "example-chat", Last: 2000}))
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, m := range resp.Msg.GetMessages() {
			if m.GetExternalAuthor() != nil {
				n++
			}
		}
		return n
	}
	deadline := time.Now().Add(30 * time.Second)
	for count() < 1000 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if n := count(); n != 1000 {
		t.Fatalf("%d messages stored, want 1000", n)
	}
}
