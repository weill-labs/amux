package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	httppprof "net/http/pprof"
	"os"
	"path/filepath"
	"strings"
	"time"

	charmlog "github.com/charmbracelet/log"
	"github.com/weill-labs/amux/internal/dialutil"
)

type pprofEndpoint struct {
	sockPath string
	listener net.Listener
	server   *http.Server
	done     chan struct{}
}

func PprofSocketPath(session string) string {
	return filepath.Join(SocketDir(), session+".pprof")
}

func newPprofMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", httppprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", httppprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", httppprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", httppprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", httppprof.Trace)
	mux.Handle("/debug/pprof/allocs", httppprof.Handler("allocs"))
	mux.Handle("/debug/pprof/block", httppprof.Handler("block"))
	mux.Handle("/debug/pprof/goroutine", httppprof.Handler("goroutine"))
	mux.Handle("/debug/pprof/heap", httppprof.Handler("heap"))
	mux.Handle("/debug/pprof/mutex", httppprof.Handler("mutex"))
	mux.Handle("/debug/pprof/threadcreate", httppprof.Handler("threadcreate"))
	return mux
}

func newPprofEndpoint(sockPath string, logger *charmlog.Logger) (*pprofEndpoint, error) {
	if _, err := os.Stat(sockPath); err == nil {
		conn, dialErr := dialutil.DialUnixStaleProbe(sockPath)
		if dialErr == nil {
			conn.Close()
			return nil, fmt.Errorf("pprof debug endpoint already running at %s", sockPath)
		}
		_ = os.Remove(sockPath)
	}

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("listening on pprof socket: %w", err)
	}
	if err := os.Chmod(sockPath, 0600); err != nil {
		listener.Close()
		_ = os.Remove(sockPath)
		return nil, fmt.Errorf("chmod pprof socket: %w", err)
	}

	endpoint := &pprofEndpoint{
		sockPath: sockPath,
		listener: listener,
		server:   &http.Server{Handler: newPprofMux()},
		done:     make(chan struct{}),
	}
	go func() {
		defer close(endpoint.done)
		err := endpoint.server.Serve(listener)
		if err == nil || errors.Is(err, http.ErrServerClosed) || strings.Contains(err.Error(), "use of closed network connection") {
			return
		}
		logger.Warn("pprof debug endpoint stopped",
			"event", "pprof_stop",
			"socket", sockPath,
			"error", err,
		)
	}()

	return endpoint, nil
}

func (p *pprofEndpoint) close() {
	if p == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = p.server.Shutdown(ctx)
	_ = p.listener.Close()
	<-p.done
	_ = os.Remove(p.sockPath)
}

func (s *Server) EnablePprof() error {
	if s == nil || s.pprof != nil {
		return nil
	}
	sess := s.firstSession()
	if sess == nil {
		return fmt.Errorf("pprof debug endpoint requires an active session")
	}

	endpoint, err := newPprofEndpoint(PprofSocketPath(sess.Name), s.logger)
	if err != nil {
		return err
	}
	s.pprof = endpoint
	s.logger.Info("pprof debug endpoint enabled",
		"event", "pprof_start",
		"session", sess.Name,
		"socket", endpoint.sockPath,
	)
	return nil
}
