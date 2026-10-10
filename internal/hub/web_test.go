package hub_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/gen/agora/v1/agorav1connect"
	"github.com/vsem-azamat/agora/internal/hub"
	"github.com/vsem-azamat/agora/internal/store"
)

// webHub is a running hub that also serves the web app.
type webHub struct {
	*running
	base string // http://127.0.0.1:<port>
	web  agorav1connect.WebServiceClient
}

func startWeb(t *testing.T, operator string) *webHub {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r := startWith(t, func(h *hub.Hub) {
		if err := h.EnableWeb(context.Background(), l, operator); err != nil {
			t.Fatal(err)
		}
	})
	w := &webHub{running: r, base: "http://" + l.Addr().String()}
	w.web = agorav1connect.NewWebServiceClient(r.httpClient, "http://agora")
	return w
}

// token asks the hub's socket for the web token.
func (w *webHub) token(t *testing.T, rotate bool) string {
	t.Helper()
	resp, err := w.web.Token(context.Background(), connect.NewRequest(&agorav1.TokenRequest{Rotate: rotate}))
	if err != nil {
		t.Fatal(err)
	}
	return resp.Msg.GetToken()
}

// bearer adds a token to every request, streams included.
type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return b.next.RoundTrip(r)
}

func (w *webHub) client(token string) *http.Client {
	return &http.Client{Transport: bearer{token, http.DefaultTransport}}
}

func code(err error) connect.Code {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce.Code()
	}
	return connect.CodeUnknown
}

func TestWebCallsNeedTheToken(t *testing.T) {
	w := startWeb(t, "operator")
	list := func(token string) error {
		c := agorav1connect.NewAgentServiceClient(w.client(token), w.base)
		_, err := c.ListAgents(context.Background(), connect.NewRequest(&agorav1.ListAgentsRequest{}))
		return err
	}
	if err := list(""); code(err) != connect.CodeUnauthenticated {
		t.Fatalf("no token yet, none sent: %v", err)
	}
	token := w.token(t, false)
	if err := list(""); code(err) != connect.CodeUnauthenticated {
		t.Fatalf("no token sent: %v", err)
	}
	if err := list(token[:42] + "x"); code(err) != connect.CodeUnauthenticated {
		t.Fatalf("wrong token: %v", err)
	}
	if err := list(token); err != nil {
		t.Fatalf("right token: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, w.base+agorav1connect.AgentServiceListAgentsProcedure, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || strings.Contains(string(body), "agents") {
		t.Fatalf("plain request: %d %s", resp.StatusCode, body)
	}
}

func TestWebTokenIsKeptAndRotated(t *testing.T) {
	w := startWeb(t, "operator")
	first := w.token(t, false)
	if len(first) != 43 || strings.ContainsAny(first, "+/=") {
		t.Fatalf("token %q is not 32 bytes of unpadded base64url", first)
	}
	if again := w.token(t, false); again != first {
		t.Fatalf("token changed without rotating: %q -> %q", first, again)
	}
	resp, err := w.web.Token(context.Background(), connect.NewRequest(&agorav1.TokenRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.TrimPrefix(w.base, "http://"); resp.Msg.GetWebAddress() != want {
		t.Fatalf("web address %q, want %q", resp.Msg.GetWebAddress(), want)
	}
	webToken := agorav1connect.NewWebServiceClient(w.client(first), w.base)
	if _, err := webToken.Token(context.Background(), connect.NewRequest(&agorav1.TokenRequest{})); code(err) != connect.CodeNotFound {
		t.Fatalf("token over the web: %v", err)
	}
	rotated := w.token(t, true)
	if rotated == first || len(rotated) != 43 {
		t.Fatalf("rotated %q from %q", rotated, first)
	}
	if _, err := webToken.Whoami(context.Background(), connect.NewRequest(&agorav1.WhoamiRequest{})); code(err) != connect.CodeUnauthenticated {
		t.Fatalf("old token after rotating: %v", err)
	}
	who, err := agorav1connect.NewWebServiceClient(w.client(rotated), w.base).Whoami(context.Background(), connect.NewRequest(&agorav1.WhoamiRequest{}))
	if err != nil || who.Msg.GetName() != "operator" {
		t.Fatalf("whoami with the new token: %v %v", who, err)
	}
}

func TestWebTokenSurvivesARestart(t *testing.T) {
	dir := shortDir(t)
	run := func() (string, func()) {
		ctx, cancel := context.WithCancel(context.Background())
		db, err := store.Open(ctx, filepath.Join(dir, "agora.db"))
		if err != nil {
			t.Fatal(err)
		}
		h := hub.Open(db, nil, nil)
		tok, err := h.WebToken(ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		return tok, func() { cancel(); db.Close() }
	}
	first, stop := run()
	stop()
	second, stop := run()
	defer stop()
	if first != second {
		t.Fatalf("token changed across a restart: %q -> %q", first, second)
	}
}

func TestRotatingEndsOpenWatches(t *testing.T) {
	w := startWeb(t, "operator")
	token := w.token(t, false)
	stream, err := agorav1connect.NewWebServiceClient(w.client(token), w.base).Watch(context.Background(), connect.NewRequest(&agorav1.WatchRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !stream.Receive() {
		t.Fatalf("no first message: %v", stream.Err())
	}
	ended := make(chan struct{})
	go func() {
		for stream.Receive() {
		}
		close(ended)
	}()
	w.token(t, true)
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("watch with the old token kept running")
	}
}

func TestWebServesOnlyTheAppsCalls(t *testing.T) {
	w := startWeb(t, "operator")
	token := w.token(t, false)
	join(t, w.running, "example-app/merge", "builder", time.Hour)
	res := agorav1connect.NewResourceServiceClient(w.client(token), w.base)
	_, err := res.Release(context.Background(), connect.NewRequest(&agorav1.ReleaseRequest{Key: "example-app/merge", Holder: "builder", Agent: "operator", Force: true}))
	if code(err) != connect.CodeNotFound {
		t.Fatalf("release over the web: %v", err)
	}
	list, err := res.ListResources(context.Background(), connect.NewRequest(&agorav1.ListResourcesRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if rs := list.Msg.GetResources(); len(rs) != 1 || rs[0].GetEntries()[0].GetAgent() != "builder" {
		t.Fatalf("lock after a refused release: %v", rs)
	}
	gov := agorav1connect.NewGovernanceServiceClient(w.client(token), w.base)
	if _, err := gov.Vote(context.Background(), connect.NewRequest(&agorav1.VoteRequest{Agent: "operator", ProposalId: 1, VoteChoice: agorav1.VoteChoice_VOTE_CHOICE_YES})); code(err) != connect.CodeNotFound {
		t.Fatalf("vote over the web: %v", err)
	}
	if _, err := gov.GetCharter(context.Background(), connect.NewRequest(&agorav1.GetCharterRequest{})); err != nil {
		t.Fatalf("charter over the web: %v", err)
	}
	rooms := agorav1connect.NewRoomServiceClient(w.client(token), w.base)
	_, err = rooms.Post(context.Background(), connect.NewRequest(&agorav1.PostRequest{Room: "general", Body: strings.Repeat("x", 1<<20+10)}))
	if code(err) != connect.CodeResourceExhausted {
		t.Fatalf("request over 1 MiB: %v", err)
	}
}

func TestTheAppActsAsTheOperator(t *testing.T) {
	w := startWeb(t, "owner")
	token := w.token(t, false)
	if _, err := w.sessions.JoinName(context.Background(), connect.NewRequest(&agorav1.JoinNameRequest{Name: "builder"})); err != nil {
		t.Fatal(err)
	}
	rooms := agorav1connect.NewRoomServiceClient(w.client(token), w.base)
	if _, err := rooms.Post(context.Background(), connect.NewRequest(&agorav1.PostRequest{Agent: "builder", Room: "general", Body: "@builder please rebase"})); err != nil {
		t.Fatal(err)
	}
	hist, err := w.rooms.History(context.Background(), connect.NewRequest(&agorav1.HistoryRequest{Room: "general"}))
	if err != nil {
		t.Fatal(err)
	}
	if m := hist.Msg.GetMessages(); len(m) != 1 || m[0].GetAuthor() != "owner" {
		t.Fatalf("posted from the app: %v", m)
	}
	if _, err := w.rooms.Post(context.Background(), connect.NewRequest(&agorav1.PostRequest{Agent: "builder", Room: "general", Body: "@owner the release is ready"})); err != nil {
		t.Fatal(err)
	}
	counts, err := rooms.UnreadByRoom(context.Background(), connect.NewRequest(&agorav1.UnreadByRoomRequest{Agent: "builder"}))
	if err != nil {
		t.Fatal(err)
	}
	if c := counts.Msg.GetRooms(); len(c) != 1 || c[0].GetRoom() != "general" || c[0].GetUnread() != 1 || c[0].GetAddressed() != 1 {
		t.Fatalf("operator's unread counts: %v", c)
	}
	agents, err := agorav1connect.NewAgentServiceClient(w.client(token), w.base).ListAgents(context.Background(), connect.NewRequest(&agorav1.ListAgentsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range agents.Msg.GetAgents() {
		if a.GetName() == "owner" {
			t.Fatal("the operator shows as an active agent")
		}
	}
}

func TestTheOperatorIsNotRenamed(t *testing.T) {
	w := startWeb(t, "owner")
	_, err := w.agents.Rename(context.Background(), connect.NewRequest(&agorav1.RenameRequest{Agent: "owner", Name: "admin"}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "--web-as") {
		t.Fatalf("err = %v", err)
	}
	if _, err := w.agents.Rename(context.Background(), connect.NewRequest(&agorav1.RenameRequest{Agent: "admin", Name: "owner"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("admin exists after the refused rename: %v", err)
	}
}

func TestWhoamiNamesTheOperatorTheBoardAndGeneral(t *testing.T) {
	w := startWeb(t, "owner")
	who, err := agorav1connect.NewWebServiceClient(w.client(w.token(t, false)), w.base).Whoami(context.Background(), connect.NewRequest(&agorav1.WhoamiRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if who.Msg.GetName() != "owner" || who.Msg.GetBoard() != "agora" || who.Msg.GetGeneralRoom() != "general" {
		t.Fatalf("whoami: %v", who.Msg)
	}
}

func TestInvalidOperatorNameIsRefused(t *testing.T) {
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := hub.Open(db, nil, nil).EnableWeb(context.Background(), l, "agora"); err == nil {
		t.Fatal("the board's own name was accepted as the operator")
	}
}

func TestCrossOriginRequestsAreRefused(t *testing.T) {
	w := startWeb(t, "operator")
	token := w.token(t, false)
	call := func(origin string) page {
		req, _ := http.NewRequest(http.MethodPost, w.base+agorav1connect.WebServiceWhoamiProcedure, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return page{resp.StatusCode, resp.Header}
	}
	if resp := call("https://example.com"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("another site: %d", resp.StatusCode)
	}
	resp := call(w.base)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("own origin: %d", resp.StatusCode)
	}
	for k := range resp.Header {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			t.Fatalf("CORS header %s sent", k)
		}
	}
}

// page is what a GET returned, its body already read and closed.
type page struct {
	StatusCode int
	Header     http.Header
}

func get(t *testing.T, url string) (page, string) {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec // the test's own listener
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return page{resp.StatusCode, resp.Header}, string(body)
}

func TestThePageLoadsNothingFromElsewhere(t *testing.T) {
	w := startWeb(t, "operator")
	resp, body := get(t, w.base+"/")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "<div id=\"root\">") {
		t.Fatalf("page: %d %.200s", resp.StatusCode, body)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("CSP %q lacks %q", csp, want)
		}
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("headers %v", resp.Header)
	}
	if strings.Contains(body, "googleapis") || strings.Contains(body, "https://") {
		t.Fatalf("page refers to another site: %.300s", body)
	}
	for _, f := range []string{"/fonts/OFL-Cinzel.txt", "/fonts/OFL-Spectral.txt"} {
		resp, body := get(t, w.base+f)
		if resp.StatusCode != http.StatusOK || !strings.Contains(body, "SIL OPEN FONT LICENSE") {
			t.Fatalf("%s: %d %.80s", f, resp.StatusCode, body)
		}
	}
}

func TestManifest(t *testing.T) {
	w := startWeb(t, "operator")
	resp, body := get(t, w.base+"/manifest.webmanifest")
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "manifest+json") {
		t.Fatalf("manifest: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var m struct {
		Name    string `json:"name"`
		Display string `json:"display"`
		Icons   []struct {
			Src   string `json:"src"`
			Sizes string `json:"sizes"`
		} `json:"icons"`
	}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	sizes := map[string]bool{}
	for _, i := range m.Icons {
		if r, _ := get(t, w.base+"/"+strings.TrimPrefix(i.Src, "/")); r.StatusCode != http.StatusOK {
			t.Fatalf("icon %s: %d", i.Src, r.StatusCode)
		}
		sizes[i.Sizes] = true
	}
	if m.Name != "Agora" || m.Display != "standalone" || !sizes["192x192"] || !sizes["512x512"] {
		t.Fatalf("manifest %+v", m)
	}
}

func TestWatchSendsChangesAndIgnoresReads(t *testing.T) {
	w := startWeb(t, "operator")
	token := w.token(t, false)
	if _, err := w.sessions.JoinName(context.Background(), connect.NewRequest(&agorav1.JoinNameRequest{Name: "builder"})); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := agorav1connect.NewWebServiceClient(w.client(token), w.base).Watch(ctx, connect.NewRequest(&agorav1.WatchRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !stream.Receive() {
		t.Fatalf("no first message: %v", stream.Err())
	}
	first := stream.Msg().GetRevision()
	got := make(chan int64, 10)
	go func() {
		for stream.Receive() {
			got <- stream.Msg().GetRevision()
		}
	}()
	c := w.client(token)
	for range 3 {
		_, _ = agorav1connect.NewResourceServiceClient(c, w.base).ListResources(ctx, connect.NewRequest(&agorav1.ListResourcesRequest{}))
		_, _ = agorav1connect.NewAgentServiceClient(c, w.base).ListAgents(ctx, connect.NewRequest(&agorav1.ListAgentsRequest{}))
		_, _ = agorav1connect.NewRoomServiceClient(c, w.base).ListRooms(ctx, connect.NewRequest(&agorav1.ListRoomsRequest{}))
		_, _ = agorav1connect.NewRoomServiceClient(c, w.base).ListSubscriptions(ctx, connect.NewRequest(&agorav1.ListSubscriptionsRequest{}))
	}
	select {
	case rev := <-got:
		t.Fatalf("reading sent revision %d", rev)
	case <-time.After(1500 * time.Millisecond):
	}
	if _, err := w.rooms.Post(ctx, connect.NewRequest(&agorav1.PostRequest{Agent: "builder", Room: "general", Body: "hello"})); err != nil {
		t.Fatal(err)
	}
	select {
	case rev := <-got:
		if rev <= first {
			t.Fatalf("revision %d after %d", rev, first)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no message after a post")
	}
}

// post sends a raw Connect JSON request to the web listener.
func (w *webHub) post(t *testing.T, procedure, token, origin, body string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, w.base+procedure, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

func TestEveryOtherCallIsRefusedOverTheWeb(t *testing.T) {
	w := startWeb(t, "operator")
	token := w.token(t, false)
	refused := []string{
		agorav1connect.AgentServiceLeaveProcedure, agorav1connect.AgentServiceRenameProcedure, agorav1connect.AgentServiceUpdateProfileProcedure, agorav1connect.AgentServiceWhoProcedure,
		agorav1connect.GovernanceServiceCloseProposalProcedure, agorav1connect.GovernanceServiceProposeProcedure,
		agorav1connect.GovernanceServiceSetCharterProcedure, agorav1connect.GovernanceServiceVoteProcedure,
		agorav1connect.ResourceServiceJoinProcedure, agorav1connect.ResourceServiceReleaseProcedure, agorav1connect.ResourceServiceRenewProcedure,
		agorav1connect.ResourceServiceSetSlotsProcedure, agorav1connect.ResourceServiceWaitProcedure,
		agorav1connect.RoomServiceCreateRoomProcedure, agorav1connect.RoomServiceUnreadProcedure,
		agorav1connect.SessionServiceJoinNameProcedure, agorav1connect.SessionServiceListSessionsProcedure, agorav1connect.SessionServiceReportProcedure,
		agorav1connect.SessionServiceResolveProcedure, agorav1connect.SessionServiceWaitWakeProcedure,
		agorav1connect.WebServiceTokenProcedure,
	}
	for _, p := range refused {
		if code := w.post(t, p, token, "", "{}"); code != http.StatusNotFound {
			t.Errorf("%s over the web: %d", p, code)
		}
	}
	for _, p := range []string{"//agora.v1.WebService/Token", "/agora.v1.WebService/../WebService/Token", "/agora.v1.WebService/Token/"} {
		if code := w.post(t, p, token, "", "{}"); code == http.StatusOK {
			t.Errorf("%s over the web: %d", p, code)
		}
	}
	if code := w.post(t, agorav1connect.WebServiceWhoamiProcedure, token, "", "{}"); code != http.StatusOK {
		t.Fatalf("whoami: %d", code)
	}
}

func TestCrossOriginPostPerformsNothing(t *testing.T) {
	w := startWeb(t, "operator")
	token := w.token(t, false)
	if code := w.post(t, agorav1connect.RoomServicePostProcedure, token, "https://example.com", `{"room":"general","body":"hello"}`); code != http.StatusForbidden {
		t.Fatalf("cross-origin post: %d", code)
	}
	hist, err := w.rooms.History(context.Background(), connect.NewRequest(&agorav1.HistoryRequest{Room: "general"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(hist.Msg.GetMessages()) != 0 {
		t.Fatalf("cross-origin post was performed: %v", hist.Msg.GetMessages())
	}
}

func TestBearerSchemeIsCaseInsensitive(t *testing.T) {
	w := startWeb(t, "operator")
	token := w.token(t, false)
	req, _ := http.NewRequest(http.MethodPost, w.base+agorav1connect.WebServiceWhoamiProcedure, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("lowercase scheme: %d", resp.StatusCode)
	}
}

func TestNoDirectoryListings(t *testing.T) {
	w := startWeb(t, "operator")
	for _, p := range []string{"/assets/", "/fonts/", "/icons/"} {
		if resp, _ := get(t, w.base+p); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
}

func TestTheAppFollowsAndReadsAsTheOperator(t *testing.T) {
	w := startWeb(t, "owner")
	token := w.token(t, false)
	ctx := context.Background()
	if _, err := w.sessions.JoinName(ctx, connect.NewRequest(&agorav1.JoinNameRequest{Name: "builder"})); err != nil {
		t.Fatal(err)
	}
	if _, err := w.rooms.CreateRoom(ctx, connect.NewRequest(&agorav1.CreateRoomRequest{Name: "example-app", Purpose: "the app", Agent: "builder"})); err != nil {
		t.Fatal(err)
	}
	rooms := agorav1connect.NewRoomServiceClient(w.client(token), w.base)
	followed, err := rooms.Subscribe(ctx, connect.NewRequest(&agorav1.SubscribeRequest{Agent: "builder", Rooms: []string{"example-app"}, Follow: true}))
	if err != nil {
		t.Fatal(err)
	}
	if got := followed.Msg.GetRooms(); len(got) != 2 || got[1] != "example-app" {
		t.Fatalf("operator follows %v", got)
	}
	// the request names an agent that never joined: only the operator override makes it work
	listed, err := rooms.ListSubscriptions(ctx, connect.NewRequest(&agorav1.ListSubscriptionsRequest{Agent: "ghost"}))
	if err != nil || len(listed.Msg.GetRooms()) != 2 || listed.Msg.GetRooms()[1] != "example-app" {
		t.Fatalf("listed for the operator: %v %v", listed, err)
	}
	if _, err := rooms.Subscribe(ctx, connect.NewRequest(&agorav1.SubscribeRequest{
		Rooms: []string{"example-app"}, Follow: true, Mode: agorav1.SubscriptionMode_SUBSCRIPTION_MODE_MENTIONS,
	})); err != nil {
		t.Fatal(err)
	}
	listed, err = rooms.ListSubscriptions(ctx, connect.NewRequest(&agorav1.ListSubscriptionsRequest{}))
	if subs := listed.Msg.GetSubscriptions(); err != nil || len(subs) != 2 ||
		subs[0].GetMode() != agorav1.SubscriptionMode_SUBSCRIPTION_MODE_ALL || subs[1].GetMode() != agorav1.SubscriptionMode_SUBSCRIPTION_MODE_MENTIONS {
		t.Fatalf("subscriptions with modes: %v %v", listed, err)
	}
	if _, err := rooms.Subscribe(ctx, connect.NewRequest(&agorav1.SubscribeRequest{
		Rooms: []string{"example-app"}, Follow: true, Mode: agorav1.SubscriptionMode_SUBSCRIPTION_MODE_ALL,
	})); err != nil {
		t.Fatal(err)
	}
	first, _ := w.rooms.Post(ctx, connect.NewRequest(&agorav1.PostRequest{Agent: "builder", Room: "example-app", Body: "one"}))
	if _, err := w.rooms.Post(ctx, connect.NewRequest(&agorav1.PostRequest{Agent: "builder", Room: "example-app", Body: "two"})); err != nil {
		t.Fatal(err)
	}
	general, _ := w.rooms.Post(ctx, connect.NewRequest(&agorav1.PostRequest{Agent: "builder", Room: "general", Body: "elsewhere"}))
	if _, err := rooms.MarkRoomRead(ctx, connect.NewRequest(&agorav1.MarkRoomReadRequest{Agent: "builder", Room: "example-app", ThroughId: general.Msg.GetId()})); code(err) != connect.CodeInvalidArgument {
		t.Fatalf("a message from another room: %v", err)
	}
	if _, err := rooms.MarkRoomRead(ctx, connect.NewRequest(&agorav1.MarkRoomReadRequest{Agent: "builder", Room: "example-app", ThroughId: first.Msg.GetId()})); err != nil {
		t.Fatal(err)
	}
	counts, err := rooms.UnreadByRoom(ctx, connect.NewRequest(&agorav1.UnreadByRoomRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int32{"example-app": 1, "general": 1}
	got := map[string]int32{}
	for _, c := range counts.Msg.GetRooms() {
		got[c.GetRoom()] = c.GetUnread()
	}
	if len(got) != 2 || got["example-app"] != want["example-app"] || got["general"] != want["general"] {
		t.Fatalf("owner's unread %v, want %v", got, want)
	}
}

func TestWatchSendsEveryKindOfChangeAtMostOnceASecond(t *testing.T) {
	w := startWeb(t, "operator")
	token := w.token(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := agorav1connect.NewWebServiceClient(w.client(token), w.base).Watch(ctx, connect.NewRequest(&agorav1.WatchRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !stream.Receive() {
		t.Fatal(stream.Err())
	}
	got := make(chan time.Time, 100)
	go func() {
		for stream.Receive() {
			got <- time.Now()
		}
	}()
	gov := agorav1connect.NewGovernanceServiceClient(w.httpClient, "http://agora")
	changes := []struct {
		name string
		do   func() error
	}{
		{"join", func() error {
			_, err := w.sessions.JoinName(ctx, connect.NewRequest(&agorav1.JoinNameRequest{Name: "builder"}))
			return err
		}},
		{"profile", func() error {
			task := "fix the login form"
			_, err := w.agents.UpdateProfile(ctx, connect.NewRequest(&agorav1.UpdateProfileRequest{Agent: "builder", Task: &task}))
			return err
		}},
		{"room", func() error {
			_, err := w.rooms.CreateRoom(ctx, connect.NewRequest(&agorav1.CreateRoomRequest{Name: "example-app", Purpose: "the app", Agent: "builder"}))
			return err
		}},
		{"follow", func() error {
			_, err := w.rooms.Subscribe(ctx, connect.NewRequest(&agorav1.SubscribeRequest{Agent: "operator", Rooms: []string{"example-app"}, Follow: true}))
			return err
		}},
		{"proposal", func() error {
			_, err := gov.Propose(ctx, connect.NewRequest(&agorav1.ProposeRequest{Agent: "builder", Title: "Merge under the lock", Body: "Take the merge lock first."}))
			return err
		}},
		{"vote", func() error {
			_, err := gov.Vote(ctx, connect.NewRequest(&agorav1.VoteRequest{Agent: "builder", ProposalId: 1, VoteChoice: agorav1.VoteChoice_VOTE_CHOICE_YES}))
			return err
		}},
		{"mark read", func() error {
			id, err := w.rooms.Post(ctx, connect.NewRequest(&agorav1.PostRequest{Agent: "builder", Room: "example-app", Body: "hello"}))
			if err != nil {
				return err
			}
			<-got // the post itself
			_, err = w.rooms.MarkRoomRead(ctx, connect.NewRequest(&agorav1.MarkRoomReadRequest{Agent: "operator", Room: "example-app", ThroughId: id.Msg.GetId()}))
			return err
		}},
	}
	last := time.Now()
	for _, c := range changes {
		if err := c.do(); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		select {
		case at := <-got:
			if gap := at.Sub(last); gap < hub.WatchGap-100*time.Millisecond {
				t.Fatalf("%s: messages %v apart", c.name, gap)
			}
			last = at
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: no message", c.name)
		}
	}
}
