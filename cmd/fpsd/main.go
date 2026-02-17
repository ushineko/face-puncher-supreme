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
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/ushineko/face-puncher-supreme/internal/blocklist"
	"github.com/ushineko/face-puncher-supreme/internal/config"
	"github.com/ushineko/face-puncher-supreme/internal/logbuf"
	"github.com/ushineko/face-puncher-supreme/internal/logging"
	"github.com/ushineko/face-puncher-supreme/internal/mitm"
	"github.com/ushineko/face-puncher-supreme/internal/plugin"
	"github.com/ushineko/face-puncher-supreme/internal/probe"
	"github.com/ushineko/face-puncher-supreme/internal/proxy"
	"github.com/ushineko/face-puncher-supreme/internal/stats"
	"github.com/ushineko/face-puncher-supreme/internal/transparent"
	"github.com/ushineko/face-puncher-supreme/internal/version"
	"github.com/ushineko/face-puncher-supreme/web"
)

var (
	// CLI flags — these override config file values when explicitly set.
	flagAddr          string
	flagLogDir        string
	flagVerbose       bool
	flagBlocklistURLs []string
	flagDataDir       string
	flagConfigPath    string
	flagForceCA       bool

	// Dashboard CLI flags.
	flagDashboardUser string
	flagDashboardPass string
	flagDashboardDev  bool
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

var generateCACmd = &cobra.Command{
	Use:   "generate-ca",
	Short: "Generate a CA certificate and private key for MITM interception",
	RunE:  runGenerateCA,
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&flagConfigPath, "config", "c", "", "config file path (default: fpsd.yml in current directory)")
	rootCmd.PersistentFlags().StringArrayVar(&flagBlocklistURLs, "blocklist-url", nil, "blocklist URL (repeatable)")
	rootCmd.PersistentFlags().StringVar(&flagDataDir, "data-dir", "", "directory for blocklist.db")

	rootCmd.Flags().StringVarP(&flagAddr, "addr", "a", "", "listen address (host:port)")
	rootCmd.Flags().StringVar(&flagLogDir, "log-dir", "", "directory for log files (empty to disable file logging)")
	rootCmd.Flags().BoolVarP(&flagVerbose, "verbose", "v", false, "enable verbose (DEBUG) logging")

	rootCmd.Flags().StringVar(&flagDashboardUser, "dashboard-user", "", "dashboard login username")
	rootCmd.Flags().StringVar(&flagDashboardPass, "dashboard-pass", "", "dashboard login password")
	rootCmd.Flags().BoolVar(&flagDashboardDev, "dashboard-dev", false, "serve dashboard from filesystem (development mode)")

	generateCACmd.Flags().BoolVar(&flagForceCA, "force", false, "overwrite existing CA files")

	configCmd.AddCommand(configDumpCmd)
	configCmd.AddCommand(configValidateCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(updateBlocklistCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(generateCACmd)
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
	if cmd.Flags().Changed("dashboard-user") {
		overrides.DashboardUser = &flagDashboardUser
	}
	if cmd.Flags().Changed("dashboard-pass") {
		overrides.DashboardPassword = &flagDashboardPass
	}

	cfg.Merge(overrides)

	if err := cfg.Validate(); err != nil {
		return cfg, err
	}

	return cfg, nil
}

func runProxy(cmd *cobra.Command, _ []string) error { //nolint:gocognit,gocyclo,cyclop // main entry point
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	// Create log buffer for dashboard live log viewer.
	logBuf := logbuf.New(1000)

	logResult := logging.Setup(logging.Config{
		LogDir:        cfg.LogDir,
		Verbose:       cfg.Verbose,
		ExtraHandlers: []slog.Handler{logBuf.Handler()},
	})
	defer logResult.Cleanup()
	logger := logResult.Logger

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

	// Load allowlist from config (must be set before AddInlineDomains so
	// allowlist takes priority in IsBlocked checks).
	bl.SetAllowlist(cfg.Allowlist)

	// Merge inline blocklist entries from config into in-memory map.
	bl.AddInlineDomains(cfg.Blocklist)

	logger.Info("blocklist loaded",
		"domains", bl.Size(),
		"sources", bl.SourceCount(),
		"inline_domains", len(cfg.Blocklist),
		"allowlist_entries", bl.AllowlistSize(),
		"db_path", dbPath,
	)

	var blocker proxy.Blocker
	var blockDataFn func() *probe.BlockData

	if bl.Size() > 0 || bl.AllowlistSize() > 0 {
		blocker = bl
		blockDataFn = makeBlockDataFn(bl)
	}

	// Initialize stats collector (always active for in-memory counters).
	collector := stats.NewCollector()

	// Initialize MITM interceptor if domains are configured.
	var mitmInterceptor *mitm.Interceptor
	var caPEMHandler http.HandlerFunc
	var mitmDataFn func() *probe.MITMData

	if len(cfg.MITM.Domains) > 0 {
		certPath := filepath.Join(cfg.DataDir, cfg.MITM.CACert)
		keyPath := filepath.Join(cfg.DataDir, cfg.MITM.CAKey)

		ca, caErr := mitm.LoadCA(certPath, keyPath)
		if caErr != nil {
			return fmt.Errorf("mitm: %w (run 'fpsd generate-ca' to create CA files)", caErr)
		}

		// Warn about domains in both MITM and blocklist.
		for _, d := range cfg.MITM.Domains {
			if bl.Size() > 0 && bl.IsBlocked(strings.ToLower(d)) {
				logger.Warn("mitm domain is also in blocklist (will be blocked, not intercepted)",
					"domain", d,
				)
			}
		}

		mitmInterceptor = mitm.NewInterceptor(&mitm.InterceptorConfig{
			CA:             ca,
			Domains:        cfg.MITM.Domains,
			Logger:         logger,
			Verbose:        cfg.Verbose,
			ConnectTimeout: cfg.Timeouts.Connect.Duration,
			OnMITMRequest:  collector.RecordMITMRequest,
		})

		// CA cert download handler.
		caPEM := ca.CertPEM
		caPEMHandler = func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/x-pem-file")
			w.Header().Set("Content-Disposition", "attachment; filename=fps-ca.pem")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(caPEM) //nolint:gosec // best-effort response
		}

		mitmDataFn = func() *probe.MITMData {
			return &probe.MITMData{
				Enabled:           true,
				InterceptsTotal:   mitmInterceptor.InterceptsTotal.Load(),
				DomainsConfigured: mitmInterceptor.Domains(),
			}
		}

		// Check CA expiry.
		daysUntilExpiry := time.Until(ca.NotAfter).Hours() / 24
		if daysUntilExpiry < 30 {
			logger.Warn("mitm CA certificate expires soon",
				"expires", ca.NotAfter.Format("2006-01-02"),
				"days_remaining", int(daysUntilExpiry),
			)
		}

		logger.Info("mitm enabled",
			"domains", len(cfg.MITM.Domains),
			"domain_list", cfg.MITM.Domains,
			"ca_fingerprint", ca.Fingerprint,
			"ca_expires", ca.NotAfter.Format("2006-01-02"),
		)
	} else {
		logger.Info("mitm disabled")
	}

	// Initialize content filter plugins.
	pluginsDataFn, err := initPlugins(&cfg, mitmInterceptor, collector, logger)
	if err != nil {
		return err
	}

	// Initialize stats DB if enabled.
	var statsDB *stats.DB
	var statsProvider *probe.StatsProvider
	if cfg.Stats.Enabled {
		statsDBPath := filepath.Join(cfg.DataDir, "stats.db")
		statsDB, err = stats.Open(statsDBPath, collector, logger, cfg.Stats.FlushInterval.Duration)
		if err != nil {
			return fmt.Errorf("open stats db: %w", err)
		}
		defer statsDB.Close() //nolint:errcheck // best-effort on shutdown (includes final flush)

		statsDB.SetAllowStatsSource(bl.SnapshotAllowCounts)

		logger.Info("stats database initialized",
			"path", statsDBPath,
			"flush_interval", cfg.Stats.FlushInterval.Duration,
		)
	}

	// Build transparent data callback.
	var transparentDataFn func() *probe.TransparentData
	if cfg.Transparent.Enabled {
		transparentDataFn = func() *probe.TransparentData {
			return &probe.TransparentData{
				Enabled:   true,
				HTTPAddr:  cfg.Transparent.HTTPAddr,
				HTTPSAddr: cfg.Transparent.HTTPSAddr,
			}
		}
		if mitmInterceptor == nil {
			logger.Info("transparent mode enabled without MITM — HTTPS domains will be tunneled only")
		}
	}

	// Create the proxy server with placeholder handlers (replaced after srv exists).
	srv := proxy.New(&proxy.Config{
		ListenAddr:        cfg.Listen,
		Logger:            logger,
		Verbose:           cfg.Verbose,
		Blocker:           blocker,
		MITMInterceptor:   mitmInterceptor,
		ConnectTimeout:    cfg.Timeouts.Connect.Duration,
		ReadHeaderTimeout: cfg.Timeouts.ReadHeader.Duration,
		ManagementPrefix:  cfg.Management.PathPrefix,
		HeartbeatHandler:  http.NotFound, // placeholder
		StatsHandler:      http.NotFound, // placeholder
		CAPEMHandler:      caPEMHandler,
		OnRequest:         collector.RecordRequest,
		OnTunnelClose:     collector.RecordBytes,
	})

	// Now build real handlers with the actual ServerInfo (srv).
	heartbeatHandler := probe.HeartbeatHandler(srv, blockDataFn, mitmDataFn, transparentDataFn, pluginsDataFn)
	var statsHandler http.HandlerFunc
	if cfg.Stats.Enabled {
		statsProvider = &probe.StatsProvider{
			Info:          srv,
			BlockFn:       blockDataFn,
			MITMFn:        mitmDataFn,
			TransparentFn: transparentDataFn,
			PluginsFn:     pluginsDataFn,
			StatsDB:   statsDB,
			Collector: collector,
			Resolver:  probe.NewReverseDNS(5 * time.Minute),
		}
		statsHandler = probe.StatsHandler(statsProvider)
	} else {
		statsHandler = probe.StatsDisabledHandler()
	}
	srv.SetHandlers(heartbeatHandler, statsHandler)

	// Initialize dashboard if credentials are configured.
	if cfg.Dashboard.Username != "" && cfg.Dashboard.Password != "" {
		dashboard := web.NewDashboard(&web.DashboardConfig{
			PathPrefix: cfg.Management.PathPrefix,
			Username:   cfg.Dashboard.Username,
			Password:   cfg.Dashboard.Password,
			DevMode:    flagDashboardDev,
			LogBuffer:  logBuf,
			HeartbeatJSON: func() ([]byte, error) {
				resp := probe.BuildHeartbeat(srv, blockDataFn, mitmDataFn, transparentDataFn, pluginsDataFn)
				return json.Marshal(resp)
			},
			StatsJSON: func() ([]byte, error) {
				if statsProvider != nil {
					resp := probe.BuildStats(statsProvider, 25, nil)
					return json.Marshal(resp)
				}
				return json.Marshal(map[string]string{"status": "stats disabled"})
			},
			ConfigJSON: func() ([]byte, error) {
				redacted := cfg.Redacted()
				return json.Marshal(redacted)
			},
			ReloadFn: makeReloadFn(&cfg, bl, logBuf, logResult.LevelVar, logger),
			Logger:   logger,
		})
		dashboard.Start()
		defer dashboard.Stop()
		srv.SetDashboardHandler(dashboard)
		logger.Info("dashboard enabled",
			"url", "http://"+cfg.Listen+cfg.Management.PathPrefix+"/dashboard/",
			"dev_mode", flagDashboardDev,
		)
	} else {
		logger.Info("dashboard disabled (no credentials configured)")
	}

	// Start stats flush loop.
	if statsDB != nil {
		statsDB.Start()
	}

	// Initialize transparent proxy listener if enabled.
	var tpListener *transparent.Listener
	if cfg.Transparent.Enabled {
		tpListener = transparent.New(&transparent.Config{
			HTTPAddr:        cfg.Transparent.HTTPAddr,
			HTTPSAddr:       cfg.Transparent.HTTPSAddr,
			Logger:          logger,
			Verbose:         cfg.Verbose,
			Blocker:         blocker,
			MITMInterceptor: mitmInterceptor,
			ConnectTimeout:  cfg.Timeouts.Connect.Duration,
			OnRequest:       collector.RecordRequest,
			OnTunnelClose:   collector.RecordBytes,
			OnTransparentHTTP: func() {
				collector.TransparentHTTP.Add(1)
			},
			OnTransparentTLS: func() {
				collector.TransparentTLS.Add(1)
			},
			OnTransparentMITM: func() {
				collector.TransparentMITM.Add(1)
			},
			OnTransparentBlock: func() {
				collector.TransparentBlock.Add(1)
			},
			OnSNIMissing: func() {
				collector.SNIMissing.Add(1)
			},
		})

		logger.Info("transparent proxy enabled",
			"http_addr", cfg.Transparent.HTTPAddr,
			"https_addr", cfg.Transparent.HTTPSAddr,
		)
	}

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
			"inline_blocklist", len(cfg.Blocklist),
			"allowlist_entries", bl.AllowlistSize(),
			"stats_enabled", cfg.Stats.Enabled,
			"transparent_enabled", cfg.Transparent.Enabled,
		)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Start transparent listeners in a separate goroutine.
	if tpListener != nil {
		go func() {
			if err := tpListener.ListenAndServe(); err != nil {
				logger.Error("transparent listener error", "error", err)
			}
		}()
	}

	<-ctx.Done()
	logger.Info("shutdown signal received")

	// Stop transparent listeners first.
	if tpListener != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Timeouts.Shutdown.Duration)
		tpListener.Shutdown(shutdownCtx)
		cancel()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Timeouts.Shutdown.Duration)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown error: %w", err)
	}

	logger.Info("proxy stopped")
	return nil
}

// makeReloadFn creates a function that re-reads config and hot-reloads subsystems.
func makeReloadFn(
	currentCfg *config.Config,
	bl *blocklist.DB,
	logBuf *logbuf.Buffer,
	levelVar *slog.LevelVar,
	logger *slog.Logger,
) func() error {
	return func() error {
		newCfg, _, err := config.Load(flagConfigPath)
		if err != nil {
			return fmt.Errorf("reload: %w", err)
		}
		if err := newCfg.Validate(); err != nil {
			return fmt.Errorf("reload: %w", err)
		}

		// Update allowlist.
		bl.SetAllowlist(newCfg.Allowlist)

		// Update inline blocklist (additive — new domains merged in).
		bl.AddInlineDomains(newCfg.Blocklist)

		// Update verbose mode.
		if newCfg.Verbose {
			levelVar.Set(slog.LevelDebug)
		} else {
			levelVar.Set(slog.LevelInfo)
		}

		// Resize log buffer if capacity changed (future config field).
		// Currently a no-op but prevents logBuf from being flagged unused.
		logBuf.Resize(1000)

		*currentCfg = newCfg
		logger.Info("configuration reloaded",
			"allowlist_entries", bl.AllowlistSize(),
			"verbose", newCfg.Verbose,
		)
		return nil
	}
}

func runUpdateBlocklist(cmd *cobra.Command, _ []string) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	logResult := logging.Setup(logging.Config{
		Verbose: true,
	})
	defer logResult.Cleanup()
	logger := logResult.Logger

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

func runConfigDump(cmd *cobra.Command, _ []string) error {
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

func runConfigValidate(cmd *cobra.Command, _ []string) error {
	_, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	fmt.Println("config: valid")
	return nil
}

func runGenerateCA(cmd *cobra.Command, _ []string) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	certPath := filepath.Join(cfg.DataDir, cfg.MITM.CACert)
	keyPath := filepath.Join(cfg.DataDir, cfg.MITM.CAKey)

	if err := mitm.GenerateCA(certPath, keyPath, flagForceCA); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "CA certificate: %s\n", certPath)
	fmt.Fprintf(os.Stderr, "CA private key: %s\n", keyPath)
	fmt.Fprintln(os.Stderr, "Install the CA certificate on client devices to enable MITM interception.")
	return nil
}

// initPlugins initializes content filter plugins and wires them into the MITM
// interceptor. Returns a PluginsData callback for heartbeat/stats, or nil if
// no plugins are active.
func initPlugins(
	cfg *config.Config,
	mitmInterceptor *mitm.Interceptor,
	collector *stats.Collector,
	logger *slog.Logger,
) (func() *probe.PluginsData, error) {
	if len(cfg.Plugins) == 0 || mitmInterceptor == nil {
		return nil, nil
	}

	// Convert config.PluginConf to plugin.PluginConfig.
	pluginConfigs := make(map[string]plugin.PluginConfig, len(cfg.Plugins))
	for name, pc := range cfg.Plugins {
		opts := pc.Options
		if opts == nil {
			opts = map[string]any{}
		}
		opts["data_dir"] = cfg.DataDir
		pluginConfigs[name] = plugin.PluginConfig{
			Enabled:     pc.Enabled,
			Mode:        pc.Mode,
			Placeholder: pc.Placeholder,
			Domains:     pc.Domains,
			Options:     opts,
		}
	}

	results, initErr := plugin.InitPlugins(pluginConfigs, cfg.MITM.Domains, logger)
	if initErr != nil {
		return nil, fmt.Errorf("plugin init: %w", initErr)
	}

	// Wire response modifier into MITM interceptor.
	modifier := plugin.BuildResponseModifier(results,
		func(pluginName string) {
			collector.RecordPluginInspected(pluginName)
		},
		func(pluginName, rule string, modified bool, removed int) {
			collector.RecordPluginMatch(pluginName, rule, modified, removed)
		},
		logger,
	)
	if modifier != nil {
		mitmInterceptor.ResponseModifier = modifier
	}

	logger.Info("plugins initialized", "active", len(results))

	dataFn := func() *probe.PluginsData {
		pd := &probe.PluginsData{Active: len(results)}
		for _, r := range results {
			pd.Plugins = append(pd.Plugins, probe.PluginInfo{
				Name:    r.Plugin.Name(),
				Version: r.Plugin.Version(),
				Mode:    r.Config.Mode,
				Domains: r.Config.Domains,
			})
		}
		return pd
	}
	return dataFn, nil
}

// makeBlockDataFn creates a callback that gathers block stats from the blocklist.
func makeBlockDataFn(bl *blocklist.DB) func() *probe.BlockData {
	return func() *probe.BlockData {
		return &probe.BlockData{
			Total:         bl.BlocksTotal(),
			AllowsTotal:   bl.AllowsTotal(),
			Size:          bl.Size(),
			AllowlistSize: bl.AllowlistSize(),
			Sources:       bl.SourceCount(),
		}
	}
}
