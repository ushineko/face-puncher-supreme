/*
Face Puncher Supreme - content-aware ad-blocking proxy.

Usage:

	fpsd [flags]
	fpsd version
	fpsd update-blocklist [flags]
	fpsd config dump [flags]
	fpsd config validate [flags]
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

	"github.com/spf13/cobra"
	"github.com/ushineko/face-puncher-supreme/internal/blocklist"
	"github.com/ushineko/face-puncher-supreme/internal/config"
	"github.com/ushineko/face-puncher-supreme/internal/logging"
	"github.com/ushineko/face-puncher-supreme/internal/probe"
	"github.com/ushineko/face-puncher-supreme/internal/proxy"
	"github.com/ushineko/face-puncher-supreme/internal/version"
)

var (
	// CLI flags — these override config file values when explicitly set.
	flagAddr          string
	flagLogDir        string
	flagVerbose       bool
	flagBlocklistURLs []string
	flagDataDir       string
	flagConfigPath    string
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

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configuration management",
}

var configDumpCmd = &cobra.Command{
	Use:   "dump",
	Short: "Print the resolved configuration as YAML",
	RunE:  runConfigDump,
}

var configValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate configuration and exit",
	RunE:  runConfigValidate,
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&flagConfigPath, "config", "c", "", "config file path (default: fpsd.yml in current directory)")
	rootCmd.PersistentFlags().StringArrayVar(&flagBlocklistURLs, "blocklist-url", nil, "blocklist URL (repeatable)")
	rootCmd.PersistentFlags().StringVar(&flagDataDir, "data-dir", "", "directory for blocklist.db")

	rootCmd.Flags().StringVarP(&flagAddr, "addr", "a", "", "listen address (host:port)")
	rootCmd.Flags().StringVar(&flagLogDir, "log-dir", "", "directory for log files (empty to disable file logging)")
	rootCmd.Flags().BoolVarP(&flagVerbose, "verbose", "v", false, "enable verbose (DEBUG) logging")

	configCmd.AddCommand(configDumpCmd)
	configCmd.AddCommand(configValidateCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(updateBlocklistCmd)
	rootCmd.AddCommand(configCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// loadConfig loads and merges configuration from file and CLI flags.
func loadConfig(cmd *cobra.Command) (config.Config, error) {
	cfg, cfgPath, err := config.Load(flagConfigPath)
	if err != nil {
		return cfg, err
	}

	if cfgPath != "" {
		fmt.Fprintf(os.Stderr, "config: loaded %s\n", cfgPath)
	}

	// Build CLI overrides — only include flags that were explicitly set.
	overrides := config.CLIOverrides{}

	if cmd.Flags().Changed("addr") {
		overrides.Addr = &flagAddr
	}
	if cmd.Flags().Changed("log-dir") {
		overrides.LogDir = &flagLogDir
	}
	if cmd.Flags().Changed("verbose") {
		overrides.Verbose = &flagVerbose
	}
	if cmd.Flags().Changed("data-dir") {
		overrides.DataDir = &flagDataDir
	}
	if cmd.Flags().Changed("blocklist-url") {
		overrides.BlocklistURLs = flagBlocklistURLs
	}

	cfg.Merge(overrides)

	if err := cfg.Validate(); err != nil {
		return cfg, err
	}

	return cfg, nil
}

func runProxy(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	logger, cleanup := logging.Setup(logging.Config{
		LogDir:  cfg.LogDir,
		Verbose: cfg.Verbose,
	})
	defer cleanup()

	dbPath := filepath.Join(cfg.DataDir, "blocklist.db")

	// Open or create the blocklist database.
	bl, err := blocklist.Open(dbPath, logger)
	if err != nil {
		return fmt.Errorf("open blocklist: %w", err)
	}
	defer bl.Close() //nolint:errcheck // best-effort on shutdown

	// If blocklist URLs are configured and no existing data, fetch on first run.
	if len(cfg.BlocklistURLs) > 0 && bl.Size() == 0 {
		logger.Info("first run with blocklist URLs, fetching lists...")
		if updateErr := bl.Update(cfg.BlocklistURLs, blocklist.HTTPFetcher()); updateErr != nil {
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

	srv := proxy.New(&proxy.Config{
		ListenAddr:        cfg.Listen,
		Logger:            logger,
		Verbose:           cfg.Verbose,
		Blocker:           blocker,
		BlockDataFn:       blockDataFn,
		ConnectTimeout:    cfg.Timeouts.Connect.Duration,
		ReadHeaderTimeout: cfg.Timeouts.ReadHeader.Duration,
		ManagementPrefix:  cfg.Management.PathPrefix,
	})

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("proxy starting",
			"version", version.Full(),
			"addr", cfg.Listen,
			"log_dir", cfg.LogDir,
			"verbose", cfg.Verbose,
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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Timeouts.Shutdown.Duration)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown error: %w", err)
	}

	logger.Info("proxy stopped")
	return nil
}

func runUpdateBlocklist(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	logger, cleanup := logging.Setup(logging.Config{
		Verbose: true,
	})
	defer cleanup()

	if len(cfg.BlocklistURLs) == 0 {
		return fmt.Errorf("no blocklist URLs configured (use --blocklist-url or config file)")
	}

	dbPath := filepath.Join(cfg.DataDir, "blocklist.db")

	bl, err := blocklist.Open(dbPath, logger)
	if err != nil {
		return fmt.Errorf("open blocklist: %w", err)
	}
	defer bl.Close() //nolint:errcheck // best-effort on shutdown

	if err := bl.Update(cfg.BlocklistURLs, blocklist.HTTPFetcher()); err != nil {
		return fmt.Errorf("update blocklist: %w", err)
	}

	logger.Info("blocklist update complete",
		"domains", bl.Size(),
		"sources", bl.SourceCount(),
		"db_path", dbPath,
	)

	return nil
}

func runConfigDump(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	out, err := cfg.Dump()
	if err != nil {
		return fmt.Errorf("dump config: %w", err)
	}

	fmt.Print(string(out))
	return nil
}

func runConfigValidate(cmd *cobra.Command, args []string) error {
	_, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	fmt.Println("config: valid")
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
