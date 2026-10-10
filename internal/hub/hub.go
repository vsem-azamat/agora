// Package hub serves Agora's API: the ConnectRPC services over a local socket, backed by SQLite.
package hub

import (
	"database/sql"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vsem-azamat/agora/gen/agora/v1/agorav1connect"
	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/forge"
	"github.com/vsem-azamat/agora/internal/governance"
	"github.com/vsem-azamat/agora/internal/proc"
	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/sessions"
)

const (
	// SweepEvery is how often the hub applies expired leases and claim deadlines.
	SweepEvery = time.Second
	// WakeCheckEvery is how often the hub looks for idle sessions to wake with the wake command.
	WakeCheckEvery = 10 * time.Second
	// WakeGap is the least time between two command wakes of one session.
	WakeGap = 2 * time.Minute
	// DefaultWakeSettle is how long a session stays idle before the wake command may wake it,
	// so a connector that is about to wait for it gets there first.
	DefaultWakeSettle = 10 * time.Second
	// WakeTimeout bounds one run of the wake command.
	WakeTimeout = time.Minute
	// DefaultWatchFirst is how long after starting the hub first looks up pull requests.
	DefaultWatchFirst = 10 * time.Second
	// DefaultWatchEvery is how often the hub looks up pull requests after that.
	DefaultWatchEvery = 2 * time.Minute

	// shutdownGrace is how long unary calls may finish once the hub shuts down.
	shutdownGrace = 5 * time.Second
	// readHeaderTimeout bounds reading a request's headers, on the socket and the web listener.
	readHeaderTimeout = 10 * time.Second
	// webIdleTimeout closes idle web connections.
	webIdleTimeout = 2 * time.Minute
	// maxWebHeader bounds the request headers the web listener accepts.
	maxWebHeader = 64 << 10
	// wakeKillWait is how long a killed wake command may keep its output open before the hub
	// stops reading it.
	wakeKillWait = time.Second
	// wakeOutputTail is how much of a failed wake command's output its result keeps.
	wakeOutputTail = 300
)

// Hub holds the services and the change signal that wakes streaming waiters.
type Hub struct {
	db       *sql.DB
	queue    *queue.Queue
	sessions *sessions.Sessions
	agents   *agents.Agents
	rooms    *rooms.Rooms
	gov      *governance.Governance
	alive    func(pid int, start int64) bool
	changes  *signal
	log      *slog.Logger

	// WakeCommand, when set, wakes idle sessions that no connector waits for: it runs with
	// `sh -c`, with $AGORA_TERMINAL and $AGORA_WAKE_TEXT in its environment.
	WakeCommand string
	// WakeSettle is how long a session stays idle before the wake command may wake it.
	WakeSettle time.Duration
	wakeEvery  time.Duration // how often the wake loop runs: WakeCheckEvery, shorter in tests

	// Forges, by host, are asked about the pull requests of active agents; with none the hub
	// does not follow pull requests.
	Forges forge.Forges
	// WatchFirst and WatchEvery time the pull request rounds: the first after start, then the gap.
	WatchFirst, WatchEvery time.Duration

	// webListener, when set, serves the web app acting as webAs (see EnableWeb).
	webListener net.Listener
	webAs       string
	webToken    atomic.Pointer[string] // the current web token; nil until there is one
	rotated     *signal                // fires when the web token is replaced

	waitMu  sync.Mutex
	waiters map[string]*waiter // by session id
	waking  map[string]bool    // sessions whose wake command runs now
}

// New returns a hub over the given domain services.
func New(q *queue.Queue, s *sessions.Sessions, a *agents.Agents, r *rooms.Rooms, log *slog.Logger) *Hub {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Hub{
		queue: q, sessions: s, agents: a, rooms: r, alive: proc.Alive, changes: newSignal(), rotated: newSignal(), log: log, waiters: map[string]*waiter{}, waking: map[string]bool{}, WakeSettle: DefaultWakeSettle, wakeEvery: WakeCheckEvery,
		WatchFirst: DefaultWatchFirst, WatchEvery: DefaultWatchEvery,
	}
}

// Open builds a hub over db with the given clock (time.Now when nil).
func Open(db *sql.DB, now func() time.Time, log *slog.Logger) *Hub {
	q := queue.New(db, now)
	r := rooms.New(db, now)
	h := New(q, sessions.New(db, q, r, now), agents.New(db, q, now), r, log)
	h.gov = governance.New(db, r, now)
	h.db = db
	return h
}

// Handler returns the HTTP handler with every service mounted.
func (h *Hub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(agorav1connect.NewResourceServiceHandler(&resources{h}))
	mux.Handle(agorav1connect.NewSessionServiceHandler(&sessionService{h}))
	mux.Handle(agorav1connect.NewAgentServiceHandler(&agentService{h}))
	mux.Handle(agorav1connect.NewRoomServiceHandler(&roomService{h}))
	mux.Handle(agorav1connect.NewGovernanceServiceHandler(&governanceService{h}))
	mux.Handle(agorav1connect.NewWebServiceHandler(&webService{h}))
	return mux
}
