package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	httppprof "net/http/pprof"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/weill-labs/amux/internal/dialutil"
	"github.com/weill-labs/amux/internal/proto"
)

type pprofEndpoint struct {
	session   string
	sockPath  string
	aliasPath string
	listener  net.Listener
	server    *http.Server
	done      chan struct{}
}

func PprofSocketPath(session string) string {
	return filepath.Join(proto.SocketDir(), session+".client.pprof")
}

func PprofProcessSocketPath(session string, pid int) string {
	return filepath.Join(proto.SocketDir(), fmt.Sprintf("%s.client.%d.pprof", session, pid))
}

func clientPprofSocketGlob(session string) string {
	return filepath.Join(proto.SocketDir(), session+".client.*.pprof")
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

func newPprofEndpoint(session string, pid int) (*pprofEndpoint, error) {
	if session == "" {
		return nil, fmt.Errorf("pprof debug endpoint requires an active session")
	}
	if pid <= 0 {
		return nil, fmt.Errorf("pprof debug endpoint requires a valid pid")
	}
	if err := os.MkdirAll(proto.SocketDir(), 0700); err != nil {
		return nil, fmt.Errorf("creating pprof socket dir: %w", err)
	}

	sockPath := PprofProcessSocketPath(session, pid)
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
		session:   session,
		sockPath:  sockPath,
		aliasPath: PprofSocketPath(session),
		listener:  listener,
		server:    &http.Server{Handler: newPprofMux()},
		done:      make(chan struct{}),
	}
	if err := publishPprofAlias(endpoint.aliasPath, endpoint.sockPath); err != nil {
		_ = listener.Close()
		_ = os.Remove(sockPath)
		return nil, fmt.Errorf("publishing pprof socket alias: %w", err)
	}

	go func() {
		defer close(endpoint.done)
		err := endpoint.server.Serve(listener)
		if err == nil || errors.Is(err, http.ErrServerClosed) || strings.Contains(err.Error(), "use of closed network connection") {
			return
		}
	}()

	return endpoint, nil
}

func publishPprofAlias(aliasPath, sockPath string) error {
	tmpPath := fmt.Sprintf("%s.%d.tmp", aliasPath, os.Getpid())
	_ = os.Remove(tmpPath)
	if err := os.Symlink(sockPath, tmpPath); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, aliasPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func aliasPointsToSocket(aliasPath, sockPath string) bool {
	target, err := os.Readlink(aliasPath)
	return err == nil && target == sockPath
}

func promoteFallbackPprofAlias(session, aliasPath, skipPath string) {
	matches, err := filepath.Glob(clientPprofSocketGlob(session))
	if err != nil {
		_ = os.Remove(aliasPath)
		return
	}

	type candidate struct {
		path    string
		modTime time.Time
	}
	candidates := make([]candidate, 0, len(matches))
	for _, path := range matches {
		if path == skipPath {
			continue
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			_ = os.Remove(path)
			continue
		}
		candidates = append(candidates, candidate{path: path, modTime: info.ModTime()})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime.After(candidates[j].modTime)
	})

	for _, candidate := range candidates {
		conn, err := dialutil.DialUnixStaleProbe(candidate.path)
		if err != nil {
			_ = os.Remove(candidate.path)
			continue
		}
		conn.Close()
		if err := publishPprofAlias(aliasPath, candidate.path); err == nil {
			return
		}
	}
	_ = os.Remove(aliasPath)
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
	if aliasPointsToSocket(p.aliasPath, p.sockPath) {
		_ = os.Remove(p.aliasPath)
		promoteFallbackPprofAlias(p.session, p.aliasPath, p.sockPath)
	}
}
