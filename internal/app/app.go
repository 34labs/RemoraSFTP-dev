// Package app wires the local engine services together: configuration,
// credential vault, trust store, connection manager, transfer manager, event
// bus, and the loopback HTTP server. Both the GUI server mode and the CLI
// one-shot commands build an Engine and call the same services - the browser
// is literally just another client of these services.
package app

import (
	"context"
	"fmt"
	"time"

	"remorasftp/internal/apppaths"
	"remorasftp/internal/config"
	"remorasftp/internal/credentials"
	"remorasftp/internal/events"
	"remorasftp/internal/manager"
	"remorasftp/internal/server"
	"remorasftp/internal/transfers"
	"remorasftp/internal/trust"
)

// Engine bundles the running services.
type Engine struct {
	Config   *config.Store
	Creds    credentials.Provider
	Trust    *trust.Store
	Bus      *events.Bus
	Manager  *manager.Manager
	Transfer *transfers.Manager
	Server   *server.Server

	dataDir string
	started bool
}

// New constructs all services without starting networking.
func New() (*Engine, error) {
	dataDir, err := apppaths.Dir()
	if err != nil {
		return nil, err
	}
	if _, err := apppaths.TempDir(); err != nil {
		return nil, err
	}
	// NOTE: we deliberately do NOT wipe the shared temp directory on every
	// engine construction, because one-shot CLI commands build an engine in
	// the same data dir as a possibly running GUI server. Stale .part files
	// are isolated per job and cleaned by the transfer manager and by a
	// full server shutdown.

	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	creds, err := credentials.New(dataDir)
	if err != nil {
		return nil, err
	}
	trustStore, err := trust.Open(dataDir)
	if err != nil {
		return nil, err
	}
	bus := events.NewBus(dataDir, cfg.Settings().LogLevel)
	mgr := manager.New(cfg, creds, trustStore, bus)
	tm := transfers.New(bus, mgr, cfg.Settings().ConcurrentTransfers)
	srv := server.New(server.Options{
		Config:    cfg,
		Manager:   mgr,
		Transfers: tm,
		Bus:       bus,
	})

	return &Engine{
		Config:   cfg,
		Creds:    creds,
		Trust:    trustStore,
		Bus:      bus,
		Manager:  mgr,
		Transfer: tm,
		Server:   srv,
		dataDir:  dataDir,
	}, nil
}

// DataDir returns the resolved application data directory.
func (e *Engine) DataDir() string { return e.dataDir }

// CredentialBackend reports which secret storage backend is active.
func (e *Engine) CredentialBackend() string { return e.Creds.Name() }

// Start binds the loopback HTTP server.
func (e *Engine) Start(addr string, port int) error {
	if err := e.Server.Start(addr, port); err != nil {
		return err
	}
	e.started = true
	return nil
}

// BrowserURL returns the one-time bootstrap URL for opening the UI.
func (e *Engine) BrowserURL() string { return e.Server.BrowserURL() }

// Addr returns the bound listener address.
func (e *Engine) Addr() string { return e.Server.Addr() }

// Shutdown stops everything cleanly: transfers, sessions, HTTP server. For
// one-shot CLI commands that never started the listener it leaves any
// co-running server's temp data untouched.
func (e *Engine) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e.Transfer.Shutdown(ctx)
	e.Manager.Shutdown()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutCancel()
	_ = e.Server.Shutdown(shutCtx)
	if e.started {
		_ = apppaths.WipeTemp()
	}
}

// ListenSettings returns the bind address/port honoring the opt-in remote
// access setting (loopback by default).
func (e *Engine) ListenSettings() (string, int) {
	st := e.Config.Settings()
	addr := "127.0.0.1"
	port := 0
	if st.RemoteAccess && st.ListenAddress != "" && st.ListenAddress != "127.0.0.1" {
		// Explicit advanced opt-in only.
		addr = st.ListenAddress
	}
	return addr, port
}

// StatusLine returns a human-readable startup banner line.
func (e *Engine) StatusLine() string {
	return fmt.Sprintf("RemoraSFTP engine ready - local UI: %s", e.Addr())
}
