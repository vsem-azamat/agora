package hub

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

// Sweep applies expired leases and claim deadlines every sweepEvery until ctx ends.
func (h *Hub) Sweep(ctx context.Context) {
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			changed, err := h.queue.Sweep(ctx)
			if err != nil && ctx.Err() == nil {
				h.log.Error("sweep", "err", err)
			}
			ended, err := h.sessions.EndDead(ctx, h.alive)
			if err != nil && ctx.Err() == nil {
				h.log.Error("sweep sessions", "err", err)
			}
			if len(ended) > 0 {
				h.log.Info("sessions ended: process gone", "sessions", ended)
			}
			if len(changed) > 0 {
				h.log.Info("leases or offers expired", "resources", changed)
			}
			if len(changed) > 0 || len(ended) > 0 {
				h.changes.fire()
			}
		}
	}
}

// Serve runs the hub on l until ctx ends. On shutdown it ends open streams, lets unary calls
// finish for up to shutdownGrace, and returns only after the server and every background loop
// have stopped, so nothing writes to the database the caller closes next.
func (h *Hub) Serve(ctx context.Context, l net.Listener) error {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	// Streams outlive ctx: they end on endStreams during shutdown.
	base, endStreams := context.WithCancel(context.WithoutCancel(ctx))
	defer endStreams()
	srv := &http.Server{
		Handler:           h.Handler(),
		Protocols:         protocols,
		ReadHeaderTimeout: readHeaderTimeout,
		BaseContext:       func(net.Listener) context.Context { return base },
	}
	loopCtx, stopLoops := context.WithCancel(ctx)
	var loops sync.WaitGroup
	defer func() {
		stopLoops()
		loops.Wait()
	}()
	loops.Go(func() { h.Sweep(loopCtx) })
	var webSrv *http.Server
	if h.webListener != nil {
		webSrv = &http.Server{
			Handler:           h.WebHandler(),
			ReadHeaderTimeout: readHeaderTimeout,
			IdleTimeout:       webIdleTimeout,
			MaxHeaderBytes:    maxWebHeader,
			BaseContext:       func(net.Listener) context.Context { return base },
		}
		go func() {
			if err := webSrv.Serve(h.webListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				h.log.Error("web listener", "err", err)
			}
		}()
	}
	if h.WakeCommand != "" {
		loops.Go(func() { h.wakeLoop(loopCtx) })
	}
	if len(h.Forges) > 0 && h.db != nil {
		loops.Go(func() { h.watchLoop(loopCtx) })
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-loopCtx.Done() // ctx ended, or Serve failed and returns
		endStreams()     // waiting streams return; clients reconnect to the next hub
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
		defer cancel()
		if webSrv != nil {
			_ = webSrv.Shutdown(shutdown) // like the socket server below
		}
		_ = srv.Shutdown(shutdown) // past the deadline Serve has returned; what is left is closed with the process
	}()
	err := srv.Serve(l)
	if !errors.Is(err, http.ErrServerClosed) {
		if webSrv != nil {
			_ = webSrv.Close() // the socket failed: the caller closes the database next
		}
		stopLoops()
	}
	<-stopped
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// signal lets any number of goroutines wait for the next change; rev counts the changes.
type signal struct {
	mu  sync.Mutex
	ch  chan struct{}
	rev int64
}

func newSignal() *signal { return &signal{ch: make(chan struct{})} }

func (s *signal) wait() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ch
}

// waitRevision is wait, also returning how many changes there were before.
func (s *signal) waitRevision() (<-chan struct{}, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ch, s.rev
}

func (s *signal) fire() {
	s.mu.Lock()
	s.rev++
	close(s.ch)
	s.ch = make(chan struct{})
	s.mu.Unlock()
}
