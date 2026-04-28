package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

func newHTTPServer(config Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:    config.Addr,
		Handler: handler,
	}
}

func runHTTPServer(ctx context.Context, server *http.Server, shutdownTimeout time.Duration) error {
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	return runHTTPServerOnListener(ctx, server, listener, shutdownTimeout)
}

func runHTTPServerOnListener(ctx context.Context, server *http.Server, listener net.Listener, shutdownTimeout time.Duration) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownCtx)
		serveErr := <-errCh
		if shutdownErr != nil {
			return shutdownErr
		}
		if errors.Is(serveErr, http.ErrServerClosed) {
			return nil
		}
		return serveErr
	}
}
