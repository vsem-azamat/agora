package hub

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/gen/agora/v1/agorav1connect"
	"github.com/vsem-azamat/agora/internal/web"
)

const (
	// watchGap is the least time between two messages of a watch.
	watchGap = time.Second
	// maxWebRequest is the largest request body the web listener accepts.
	maxWebRequest = 1 << 20
)

// webProcedures are the calls the web listener serves; every other call is not found there.
var webProcedures = map[string]bool{
	agorav1connect.AgentServiceListAgentsProcedure:         true,
	agorav1connect.RoomServiceListRoomsProcedure:           true,
	agorav1connect.RoomServiceHistoryProcedure:             true,
	agorav1connect.RoomServicePostProcedure:                true,
	agorav1connect.RoomServiceSubscribeProcedure:           true,
	agorav1connect.RoomServiceListSubscriptionsProcedure:   true,
	agorav1connect.RoomServiceUnreadByRoomProcedure:        true,
	agorav1connect.RoomServiceMarkRoomReadProcedure:        true,
	agorav1connect.ResourceServiceListResourcesProcedure:   true,
	agorav1connect.GovernanceServiceListProposalsProcedure: true,
	agorav1connect.GovernanceServiceGetProposalProcedure:   true,
	agorav1connect.GovernanceServiceGetCharterProcedure:    true,
	agorav1connect.WebServiceWhoamiProcedure:               true,
	agorav1connect.WebServiceWatchProcedure:                true,
}

// contentSecurityPolicy lets the app load and call only its own listener.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; " +
	"connect-src 'self'; manifest-src 'self'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// EnableWeb makes Serve also serve the web app and its API on l, acting as operator. It
// registers the operator's name, which must be a valid agent name.
func (h *Hub) EnableWeb(ctx context.Context, l net.Listener, operator string) error {
	if _, err := h.sessions.Join(ctx, operator, "", false); err != nil {
		return fmt.Errorf("--web-as: %w", err)
	}
	token, err := h.tokens.Get(ctx)
	if err != nil {
		return err
	}
	if token != "" {
		h.webToken.Store(&token)
	}
	h.webListener, h.webAs = l, operator
	return nil
}

// WebToken returns the web token, creating one when there is none; with rotate it replaces it
// and ends every web call made with the old one.
func (h *Hub) WebToken(ctx context.Context, rotate bool) (string, error) {
	token, err := h.tokens.Ensure(ctx, rotate)
	if err != nil {
		return "", err
	}
	h.webToken.Store(&token)
	if rotate {
		h.rotated.fire()
	}
	return token, nil
}

// validToken reports whether presented is the current web token, comparing in constant time.
func (h *Hub) validToken(presented string) bool {
	token := h.webToken.Load()
	return token != nil && *token != "" && subtle.ConstantTimeCompare([]byte(presented), []byte(*token)) == 1
}

// bearer returns the credentials of an `Authorization: Bearer` header; the scheme is
// case-insensitive.
func bearer(r *http.Request) (string, bool) {
	scheme, cred, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	return strings.TrimSpace(cred), ok && strings.EqualFold(scheme, "Bearer")
}

// WebHandler serves the web app's files to anyone and, to requests with the web token, the
// calls the app needs, acting as the operator.
func (h *Hub) WebHandler() http.Handler {
	opts := connect.WithHandlerOptions(
		connect.WithInterceptors(operatorInterceptor(h.webAs)),
		connect.WithReadMaxBytes(maxWebRequest),
	)
	api := http.NewServeMux()
	api.Handle(agorav1connect.NewResourceServiceHandler(&resources{h}, opts))
	api.Handle(agorav1connect.NewAgentServiceHandler(&agentService{h}, opts))
	api.Handle(agorav1connect.NewRoomServiceHandler(&roomService{h}, opts))
	api.Handle(agorav1connect.NewGovernanceServiceHandler(&governanceService{h}, opts))
	api.Handle(agorav1connect.NewWebServiceHandler(&webService{h}, opts))
	files := web.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", contentSecurityPolicy)
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("X-Frame-Options", "DENY")
		if origin := r.Header.Get("Origin"); origin != "" && !sameHost(origin, r.Host) {
			http.Error(w, "cross-origin requests are refused", http.StatusForbidden)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/agora.v1.") {
			files.ServeHTTP(w, r)
			return
		}
		hd.Set("Cache-Control", "no-store")
		rotated := h.rotated.wait() // before checking, so a rotation from here on ends the call
		token, ok := bearer(r)
		if !ok || !h.validToken(token) {
			writeConnectError(w, http.StatusUnauthorized, "unauthenticated", "a valid web token is required; see agora web token")
			return
		}
		if !webProcedures[r.URL.Path] {
			writeConnectError(w, http.StatusNotFound, "not_found", "not served on the web listener")
			return
		}
		// a rotated token ends the calls made with the old one, watches included
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		go func() {
			select {
			case <-rotated:
				cancel()
			case <-ctx.Done():
			}
		}()
		api.ServeHTTP(w, r.WithContext(ctx))
	})
}

// sameHost reports whether origin is a URL on host.
func sameHost(origin, host string) bool {
	u, err := url.Parse(origin)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && strings.EqualFold(u.Host, host)
}

// writeConnectError writes an error the way the Connect protocol does, so clients read its code.
func writeConnectError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"code":%q,"message":%q}`, code, msg)
}

// operatorInterceptor attributes everything the app does to the operator, whatever name the
// request carries.
func operatorInterceptor(operator string) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			asOperator(req.Any(), operator)
			return next(ctx, req)
		}
	}
}

// asOperator sets the acting agent of a request, its string field agent, to the operator.
// Requests without that field are left alone.
func asOperator(msg any, operator string) {
	m, ok := msg.(proto.Message)
	if !ok {
		return
	}
	r := m.ProtoReflect()
	f := r.Descriptor().Fields().ByName("agent")
	if f != nil && f.Kind() == protoreflect.StringKind && f.Cardinality() != protoreflect.Repeated {
		r.Set(f, protoreflect.ValueOfString(operator))
	}
}

// --- WebService ---------------------------------------------------------------------

type webService struct{ h *Hub }

func (s *webService) Token(ctx context.Context, req *connect.Request[agorav1.TokenRequest]) (*connect.Response[agorav1.TokenResponse], error) {
	token, err := s.h.WebToken(ctx, req.Msg.GetRotate())
	if err != nil {
		return nil, toConnect(err)
	}
	out := &agorav1.TokenResponse{Token: token}
	if s.h.webListener != nil {
		out.WebAddress = s.h.webListener.Addr().String()
	}
	return connect.NewResponse(out), nil
}

func (s *webService) Whoami(context.Context, *connect.Request[agorav1.WhoamiRequest]) (*connect.Response[agorav1.WhoamiResponse], error) {
	return connect.NewResponse(&agorav1.WhoamiResponse{Name: s.h.webAs}), nil
}

func (s *webService) Watch(ctx context.Context, _ *connect.Request[agorav1.WatchRequest], stream *connect.ServerStream[agorav1.WatchResponse]) error {
	for {
		changed, rev := s.h.changes.waitRevision()
		if err := stream.Send(&agorav1.WatchResponse{Revision: rev}); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
		select { // at most one message per watchGap; changes meanwhile are in the next one
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(watchGap):
		}
	}
}
