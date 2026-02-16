/*
Face Puncher Supreme - content-aware ad-blocking proxy.

Usage:

	fpsd [flags]
	fpsd version
	fpsd update-blocklist [flags]
*/
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/ushineko/face-puncher-supreme/internal/blocklist"
	"github.com/ushineko/face-puncher-supreme/internal/logging"
	"github.com/ushineko/face-puncher-supreme/internal/probe"
	"github.com/ushineko/face-puncher-supreme/internal/proxy"
	"github.com/ushineko/face-puncher-supreme/internal/version"
)

var (
	addr          string
	logDir        string
	verbose       bool
	blocklistURLs []string
	dataDir       string
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

var updateBlocklistCmd = &cobra.Command{
	Use:   "update-blocklist",
	Short: "Download blocklists and rebuild the database, then exit",
	RunE:  runUpdateBlocklist,
}

func init() {
	rootCmd.PersistentFlags().StringArrayVar(&blocklistURLs, "blocklist-url", nil, "blocklist URL (repeatable)")
	rootCmd.PersistentFlags().StringVar(&dataDir, "data-dir", ".", "directory for blocklist.db")

	rootCmd.Flags().StringVarP(&addr, "addr", "a", ":8080", "listen address (host:port)")
	rootCmd.Flags().StringVar(&logDir, "log-dir", "logs", "directory for log files (empty to disable file logging)")
	rootCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "enable verbose (DEBUG) logging")

	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(updateBlocklistCmd)
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

	dbPath := filepath.Join(dataDir, "blocklist.db")

	// Open or create the blocklist database.
	bl, err := blocklist.Open(dbPath, logger)
	if err != nil {
		return fmt.Errorf("open blocklist: %w", err)
	}
	defer bl.Close() //nolint:errcheck // best-effort on shutdown

	// If blocklist URLs are configured and no existing data, fetch on first run.
	if len(blocklistURLs) > 0 && bl.Size() == 0 {
		logger.Info("first run with blocklist URLs, fetching lists...")
		if updateErr := bl.Update(blocklistURLs, blocklist.HTTPFetcher()); updateErr != nil {
			logger.Error("failed to update blocklist on first run", "error", updateErr)
		}
	}

	logger.Info("blocklist loaded",
		"domains", bl.Size(),
		"sources", bl.SourceCount(),
		"db_path", dbPath,
	)

	var blocker proxy.Blocker
	var blockDataFn func() *probe.BlockData

	if bl.Size() > 0 {
		blocker = bl
		blockDataFn = makeBlockDataFn(bl)
	}

	srv := proxy.New(proxy.Config{
		ListenAddr:  addr,
		Logger:      logger,
		Verbose:     verbose,
		Blocker:     blocker,
		BlockDataFn: blockDataFn,
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
			"blocklist_domains", bl.Size(),
			"blocklist_sources", bl.SourceCount(),
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

func runUpdateBlocklist(cmd *cobra.Command, args []string) error {
	logger, cleanup := logging.Setup(logging.Config{
		Verbose: true,
	})
	defer cleanup()

	if len(blocklistURLs) == 0 {
		return fmt.Errorf("no --blocklist-url flags provided")
	}

	dbPath := filepath.Join(dataDir, "blocklist.db")

	bl, err := blocklist.Open(dbPath, logger)
	if err != nil {
		return fmt.Errorf("open blocklist: %w", err)
	}
	defer bl.Close() //nolint:errcheck // best-effort on shutdown

	if err := bl.Update(blocklistURLs, blocklist.HTTPFetcher()); err != nil {
		return fmt.Errorf("update blocklist: %w", err)
	}

	logger.Info("blocklist update complete",
		"domains", bl.Size(),
		"sources", bl.SourceCount(),
		"db_path", dbPath,
	)

	return nil
}

// makeBlockDataFn creates a callback that gathers block stats from the DB.
func makeBlockDataFn(bl *blocklist.DB) func() *probe.BlockData {
	return func() *probe.BlockData {
		top := bl.TopBlocked(10)
		entries := make([]probe.TopEntry, len(top))
		for i, e := range top {
			entries[i] = probe.TopEntry{Domain: e.Domain, Count: e.Count}
		}
		return &probe.BlockData{
			Total:   bl.BlocksTotal(),
			Size:    bl.Size(),
			Sources: bl.SourceCount(),
			Top:     entries,
		}
	}
}
