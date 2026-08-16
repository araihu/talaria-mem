package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/security"
)

var (
	ErrDaemonUnavailable = errors.New("daemon unavailable")
	ErrDaemonAddress     = errors.New("daemon address must be literal loopback")
)

// DaemonConfig is explicit so a composition root can construct exactly one
// HTTP/MCP graph and inject a test listener without opening a socket.
type DaemonConfig struct {
	Address         string
	Handler         http.Handler
	Listener        net.Listener
	Startup         func(context.Context) error
	Shutdown        func(context.Context) error
	Readiness       *Readiness
	Diagnostic      bool
	ShutdownTimeout time.Duration
}

// Daemon owns process lifecycle, not application policy.  Health is process
// liveness; readiness is supplied separately through the HTTP handler and is
// never inferred from a successful Listen call.
type Daemon struct {
	config DaemonConfig
	server *http.Server
	mu     sync.Mutex
	ln     net.Listener
	run    bool
}

func NewDaemon(configuration DaemonConfig) (*Daemon, error) {
	if configuration.Handler == nil {
		return nil, ErrDaemonUnavailable
	}
	if configuration.Listener == nil {
		if err := validateLoopbackAddress(configuration.Address); err != nil {
			return nil, err
		}
	}
	if configuration.ShutdownTimeout <= 0 {
		configuration.ShutdownTimeout = 5 * time.Second
	}
	return &Daemon{config: configuration}, nil
}

// New is a concise composition alias.
func New(configuration DaemonConfig) (*Daemon, error) { return NewDaemon(configuration) }

func (daemon *Daemon) Health() bool {
	return daemon != nil
}

func (daemon *Daemon) Listener() net.Listener {
	if daemon == nil {
		return nil
	}
	daemon.mu.Lock()
	defer daemon.mu.Unlock()
	return daemon.ln
}

// Run starts the foreground daemon and returns when the listener stops or the
// context is cancelled.  Startup runs before the listener is advertised so a
// failed recovery never presents a ready service.
func (daemon *Daemon) Run(ctx context.Context) error {
	if daemon == nil || daemon.config.Handler == nil {
		return ErrDaemonUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if daemon.config.Startup != nil {
		if err := daemon.config.Startup(ctx); err != nil {
			return err
		}
	}
	listener := daemon.config.Listener
	var err error
	if listener == nil {
		listener, err = net.Listen("tcp", daemon.config.Address)
		if err != nil {
			return fmt.Errorf("listen daemon: %w", err)
		}
	}
	daemon.mu.Lock()
	daemon.ln = listener
	daemon.run = true
	daemon.mu.Unlock()

	server := &http.Server{
		Handler:           daemon.config.Handler,
		ReadHeaderTimeout: time.Second,
		IdleTimeout:       30 * time.Second,
	}
	daemon.mu.Lock()
	daemon.server = server
	daemon.mu.Unlock()
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), daemon.config.ShutdownTimeout)
		shutdownErr := server.Shutdown(shutdownCtx)
		cancel()
		if daemon.config.Shutdown != nil {
			shutdownErr = errors.Join(shutdownErr, daemon.config.Shutdown(context.Background()))
		}
		if shutdownErr != nil {
			return shutdownErr
		}
		return nil
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Serve is an alias used by command adapters.
func (daemon *Daemon) Serve(ctx context.Context) error { return daemon.Run(ctx) }

func (daemon *Daemon) Stop(ctx context.Context) error {
	if daemon == nil {
		return nil
	}
	daemon.mu.Lock()
	server := daemon.server
	listener := daemon.ln
	daemon.mu.Unlock()
	if server != nil {
		return server.Shutdown(ctx)
	}
	if listener != nil {
		return listener.Close()
	}
	return nil
}

func validateLoopbackAddress(address string) error {
	if strings.TrimSpace(address) == "" {
		return ErrDaemonAddress
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" || (host != "127.0.0.1" && host != "::1") {
		return ErrDaemonAddress
	}
	if err := security.ValidateConfiguredHost(address); err != nil {
		return ErrDaemonAddress
	}
	return nil
}
