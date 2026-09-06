package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	hnslop "github.com/nilsherzig/hnslop"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "hnslop: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("a command is required; use 'serve'")
	}
	if args[0] != "serve" {
		return fmt.Errorf("unknown command %q; use 'serve'", args[0])
	}

	flags := flag.NewFlagSet("hnslop serve", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	host := flags.String("host", "127.0.0.1", "address to listen on")
	port := flags.Int("port", 8000, "port to listen on")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if *port < 1 || *port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}

	config, err := hnslop.LoadConfig()
	if err != nil {
		return err
	}
	database, err := hnslop.OpenDatabase(config.DatabasePath)
	if err != nil {
		return err
	}
	defer database.Close()

	proxy := hnslop.NewProxy(database, config, nil)
	defer proxy.Close()

	logger := log.New(os.Stderr, "hnslop: ", log.LstdFlags|log.Lmicroseconds)
	application := hnslop.NewApp(proxy, config, logger)
	server := &http.Server{
		Addr:              net.JoinHostPort(*host, strconv.Itoa(*port)),
		Handler:           application.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          logger,
	}

	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	logger.Printf("listening on http://%s", listener.Addr())

	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()

	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-signalContext.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return fmt.Errorf("shut down HTTP server: %w", err)
		}
		return nil
	}
}
