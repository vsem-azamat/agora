package cli

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"

	"connectrpc.com/connect"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/gen/agora/v1/agorav1connect"
)

// baseURL is the URL the RPC clients call; the client dials the socket, so the host is a
// placeholder.
const baseURL = "http://agora"

// sessionID is the agent session this command runs in, as the agent tool exposes it.
func sessionID() string {
	for _, v := range []string{"CLAUDE_CODE_SESSION_ID", "AGORA_SESSION"} {
		if id := os.Getenv(v); id != "" {
			return id
		}
	}
	return ""
}

// agent resolves who runs the command: --as, else $AGORA_NAME, else the name bound to the
// command's session.
func (o *options) agent(ctx context.Context) (string, error) {
	if strings.TrimSpace(o.as) != "" {
		return o.as, nil
	}
	if id := sessionID(); id != "" {
		resp, err := o.sessions().Resolve(ctx, connect.NewRequest(&agorav1.ResolveRequest{SessionId: id}))
		if err != nil {
			return "", err
		}
		if name := resp.Msg.GetAgent(); name != "" {
			return name, nil
		}
	}
	return "", errors.New("who are you? join first (`agora join <name>`), or pass --as <name> or set AGORA_NAME")
}

// httpClient returns the one HTTP client every RPC client of this command shares.
func (o *options) httpClient() *http.Client {
	if o.client == nil {
		socket := o.socket
		o.client = &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
		}}
	}
	return o.client
}

func (o *options) resources() agorav1connect.ResourceServiceClient {
	return agorav1connect.NewResourceServiceClient(o.httpClient(), baseURL)
}

func (o *options) sessions() agorav1connect.SessionServiceClient {
	return agorav1connect.NewSessionServiceClient(o.httpClient(), baseURL)
}

func (o *options) agents() agorav1connect.AgentServiceClient {
	return agorav1connect.NewAgentServiceClient(o.httpClient(), baseURL)
}

func (o *options) rooms() agorav1connect.RoomServiceClient {
	return agorav1connect.NewRoomServiceClient(o.httpClient(), baseURL)
}

func (o *options) governance() agorav1connect.GovernanceServiceClient {
	return agorav1connect.NewGovernanceServiceClient(o.httpClient(), baseURL)
}

func (o *options) web() agorav1connect.WebServiceClient {
	return agorav1connect.NewWebServiceClient(o.httpClient(), baseURL)
}
