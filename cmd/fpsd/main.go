/*
Face Puncher Supreme - content-aware ad-blocking proxy.

Usage:

	fpsd [flags]
	fpsd version
*/
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/ushineko/face-puncher-supreme/internal/logging"
	"github.com/ushineko/face-puncher-supreme/internal/proxy"
	"github.com/ushineko/face-puncher-supreme/internal/version"
)

var (
	addr    string
	logDir  string
	verbose bool
)

var rootCmd = &cobra.Command{
	Use:   "fpsd",
	Short: "Face Puncher Supreme - content-aware ad-blocking proxy",
	RunE:  runProxy,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(version.Full())
	},
}

func init() {
	rootCmd.Flags().StringVarP(&addr, "addr", "a", ":8080", "listen address (host:port)")
	rootCmd.Flags().StringVar(&logDir, "log-dir", "logs", "directory for log files (empty to disable file logging)")
	rootCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "enable verbose (DEBUG) logging")
	rootCmd.AddCommand(versionCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runProxy(cmd *cobra.Command, args []string) error {
	logger, cleanup := logging.Setup(logging.Config{
		LogDir:  logDir,
		Verbose: verbose,
	})
	defer cleanup()

	srv := proxy.New(proxy.Config{
		ListenAddr: addr,
		Logger:     logger,
		Verbose:    verbose,
	})

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("proxy starting",
			"version", version.Full(),
			"addr", addr,
			"log_dir", logDir,
			"verbose", verbose,
		)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown error: %w", err)
	}

	logger.Info("proxy stopped")
	return nil
}
