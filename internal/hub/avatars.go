package hub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/internal/avatars"
	"github.com/vsem-azamat/agora/internal/bridges"
)

const (
	// avatarsPerMinute is how many avatar lines a bridge may send a minute; the hub drops the rest.
	avatarsPerMinute = 30
	// avatarPath is where the web listener serves avatars, by hash.
	avatarPath = "/avatars/"
	// avatarSmall is the size query that asks for the smaller picture.
	avatarSmall = "128"
	// avatarPolicy is the content security policy of an avatar: an SVG opened on its own loads
	// and runs nothing.
	avatarPolicy = "default-src 'none'; style-src 'unsafe-inline'"
	// avatarCache lets the browser keep an avatar for a year: it never changes under its hash.
	avatarCache = "private, max-age=31536000, immutable"
)

// maxAvatarData bounds the base64 data of an avatar line: a raster image of avatars.MaxRaster.
var maxAvatarData = base64.StdEncoding.EncodedLen(avatars.MaxRaster)

// avatarHash is how an avatar's hash is written: SHA-256 in lowercase hex.
var avatarHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

// avatarRate counts the avatar lines of each bridge, across its restarts.
type avatarRate struct {
	mu     sync.Mutex
	limits map[string]*avatarLimit
}

type avatarLimit struct {
	rate       logLimit
	fullLogged bool // the hub logged that the bridge keeps the most avatars it may
}

func (r *avatarRate) limit(bridge string) *avatarLimit {
	l := r.limits[bridge]
	if l == nil {
		l = &avatarLimit{rate: logLimit{max: avatarsPerMinute, window: time.Minute}}
		r.limits[bridge] = l
	}
	return l
}

// allow reports whether the bridge may send another avatar line at now, and whether a refused
// one is the first refused in its minute.
func (r *avatarRate) allow(bridge string, now time.Time) (ok, first bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	l := r.limit(bridge)
	ok, _ = l.rate.allow(now)
	return ok, !ok && l.rate.dropped == 1
}

// full reports whether the bridge is found keeping the most avatars it may for the first time.
func (r *avatarRate) full(bridge string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	l := r.limit(bridge)
	first := !l.fullLogged
	l.fullLogged = true
	return first
}

// lineAuthor is the author of a bridge's line: an object on an in line, the author's id alone
// (a string or a number) on an avatar line.
type lineAuthor struct {
	ID        json.RawMessage `json:"id"`
	Name      string          `json:"name"`
	Self      bool            `json:"self"`
	AvatarRev json.RawMessage `json:"avatar_rev"`
}

func (a *lineAuthor) UnmarshalJSON(b []byte) error {
	if t := strings.TrimSpace(string(b)); !strings.HasPrefix(t, "{") {
		*a = lineAuthor{ID: append(json.RawMessage(nil), b...)}
		return nil
	}
	type object lineAuthor // without this method
	return json.Unmarshal(b, (*object)(a))
}

// present reports whether a raw field was given, and not as null.
func present(raw json.RawMessage) bool { return len(raw) > 0 && string(raw) != "null" }

// bridgeAvatar takes an avatar line: the avatar of a person outside, or that the person has none.
// A rev already stored is ignored before anything is decoded; lines over the bridge's rate,
// people over its limit and images the hub refuses are dropped and logged.
func (h *Hub) bridgeAvatar(ctx context.Context, name string, l bridgeInput) {
	author, rev := extString(l.Author.ID), extString(l.Rev)
	if author == "" {
		h.log.Warn("bridge avatar line without an author; skipped", "bridge", name)
		return
	}
	if len(l.Data) > maxAvatarData {
		h.log.Warn("bridge avatar too large; skipped", "bridge", name, "author", author, "max_bytes", avatars.MaxRaster)
		return
	}
	stored, ok, err := h.bridges.AvatarRev(ctx, name, author)
	if err != nil {
		h.log.Error("bridge avatar line", "bridge", name, "err", err)
		return
	}
	if ok && stored == rev {
		return
	}
	if ok, first := h.avatarRate.allow(name, time.Now()); !ok {
		if first {
			h.log.Warn("bridge sends more avatars than the hub takes; dropping them this minute", "bridge", name, "per_minute", avatarsPerMinute)
		}
		return
	}
	var p *avatars.Picture
	if l.Data != "" {
		data, err := base64.StdEncoding.DecodeString(l.Data)
		if err != nil {
			h.log.Warn("bridge avatar data is not base64; skipped", "bridge", name, "author", author)
			return
		}
		pic, err := avatars.Prepare(data, l.MIME)
		if err != nil {
			h.log.Warn("bridge avatar refused", "bridge", name, "author", author, "err", err)
			return
		}
		p = &pic
	}
	err = h.bridges.SetAvatar(ctx, name, author, rev, p)
	switch {
	case errors.Is(err, bridges.ErrAvatarsFull):
		if h.avatarRate.full(name) {
			h.log.Warn("bridge keeps the most avatars it may; avatars of new people are dropped", "bridge", name, "max", bridges.MaxOutsideAvatars)
		}
		return
	case err != nil:
		h.log.Warn("bridge avatar line; skipped", "bridge", name, "err", err)
		return
	}
	h.changes.fire()
}

// bridgeAvatarRev takes the avatar rev an in line names for its author: a different one than
// stored drops the stored avatar.
func (h *Hub) bridgeAvatarRev(ctx context.Context, name string, l bridgeInput) {
	if !present(l.Author.AvatarRev) {
		return
	}
	dropped, err := h.bridges.AvatarRevSeen(ctx, name, extString(l.Author.ID), extString(l.Author.AvatarRev))
	if err != nil {
		h.log.Error("bridge in line: avatar rev", "bridge", name, "err", err)
	}
	if dropped {
		h.changes.fire()
	}
}

// serveAvatar answers GET /avatars/<hash>[?size=128] on the web listener, which checked the
// token: the picture with its media type, under a policy that lets it load and run nothing.
func (h *Hub) serveAvatar(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	hash := strings.TrimPrefix(r.URL.Path, avatarPath)
	if !avatarHash.MatchString(hash) {
		http.NotFound(w, r)
		return
	}
	mime, data, err := h.avatars.Get(r.Context(), hash, r.URL.Query().Get("size") == avatarSmall)
	if errors.Is(err, avatars.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.log.Error("avatar", "hash", hash, "err", err)
		http.Error(w, "the avatar cannot be read", http.StatusInternalServerError)
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", mime)
	hd.Set("Content-Security-Policy", avatarPolicy)
	hd.Set("Cache-Control", avatarCache)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(data) //nolint:gosec // a picture drawn again or an SVG written anew from the allowlist, served under default-src 'none' and nosniff; a failed write means the client went away
	}
}

func (s *agentService) SetAvatar(ctx context.Context, req *connect.Request[agorav1.SetAvatarRequest]) (*connect.Response[agorav1.SetAvatarResponse], error) {
	m := req.Msg
	if _, err := s.h.avatars.SetAgent(ctx, m.GetAgent(), m.GetImage(), m.GetMime()); err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	p, err := s.h.agents.Get(ctx, m.GetAgent())
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&agorav1.SetAvatarResponse{Profile: profilePB(p)}), nil
}

func (s *agentService) ClearAvatar(ctx context.Context, req *connect.Request[agorav1.ClearAvatarRequest]) (*connect.Response[agorav1.ClearAvatarResponse], error) {
	if err := s.h.avatars.ClearAgent(ctx, req.Msg.GetAgent()); err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	p, err := s.h.agents.Get(ctx, req.Msg.GetAgent())
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&agorav1.ClearAvatarResponse{Profile: profilePB(p)}), nil
}
