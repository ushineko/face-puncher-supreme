import { useCallback, useEffect, useState } from "react";
import { fetchConfig, restartProxy } from "../api";
import { socket } from "../ws";
import { useSocket } from "../hooks/useSocket";
import RewriteRules from "../components/RewriteRules";

interface HeartbeatData {
  systemd_managed: boolean;
  plugins: string[];
}

interface ReloadResult {
  success: boolean;
  message: string;
}

type Tab = "general" | "rewrite";

export default function Config() {
  const [tab, setTab] = useState<Tab>("general");
  const heartbeat = useSocket<HeartbeatData>("heartbeat");
  const hasRewrite = heartbeat?.plugins?.some((p) => p.startsWith("rewrite@")) ?? false;
  const systemdManaged = heartbeat?.systemd_managed ?? false;

  return (
    <div className="max-w-4xl space-y-4">
      {/* Tab bar */}
      <div className="flex items-center gap-1 border-b border-vsc-border">
        <TabButton active={tab === "general"} onClick={() => setTab("general")}>
          General
        </TabButton>
        {hasRewrite && (
          <TabButton active={tab === "rewrite"} onClick={() => setTab("rewrite")}>
            Rewrite Rules
          </TabButton>
        )}
      </div>

      {tab === "general" && <GeneralTab systemdManaged={systemdManaged} />}
      {tab === "rewrite" && hasRewrite && <RewriteRules />}
    </div>
  );
}

function TabButton({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      onClick={onClick}
      className={`text-xs px-3 py-2 transition-colors border-b-2 -mb-px ${
        active
          ? "border-vsc-accent text-vsc-accent"
          : "border-transparent text-vsc-muted hover:text-vsc-fg"
      }`}
    >
      {children}
    </button>
  );
}

function GeneralTab({ systemdManaged }: { systemdManaged: boolean }) {
  const [config, setConfig] = useState<Record<string, unknown> | null>(null);
  const [error, setError] = useState("");
  const [reloading, setReloading] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [restartMsg, setRestartMsg] = useState("");
  const [confirmRestart, setConfirmRestart] = useState(false);
  const reloadResult = useSocket<ReloadResult>("reload_result");

  const loadConfig = useCallback(async () => {
    try {
      const cfg = await fetchConfig();
      setConfig(cfg);
      setError("");
    } catch (e: unknown) {
      setError((e as Error).message);
    }
  }, []);

  useEffect(() => {
    void loadConfig();
  }, [loadConfig]);

  useEffect(() => {
    if (reloadResult?.success) {
      setReloading(false);
      void loadConfig();
    } else if (reloadResult && !reloadResult.success) {
      setReloading(false);
    }
  }, [reloadResult, loadConfig]);

  function handleReload() {
    setReloading(true);
    socket.send({ type: "reload" });
  }

  async function handleRestart() {
    setRestarting(true);
    setConfirmRestart(false);
    try {
      const result = await restartProxy();
      setRestartMsg(result.message);
    } catch (e: unknown) {
      setRestartMsg((e as Error).message);
      setRestarting(false);
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm text-vsc-accent font-bold">Proxy Configuration</h2>
        <div className="flex items-center gap-2">
          {systemdManaged && (
            <>
              {confirmRestart ? (
                <div className="flex items-center gap-1">
                  <span className="text-xs text-vsc-error">Restart proxy?</span>
                  <button
                    onClick={handleRestart}
                    disabled={restarting}
                    className="text-xs bg-vsc-error/20 border border-vsc-error/40 rounded px-2 py-1 text-vsc-error hover:bg-vsc-error/30 disabled:opacity-50 transition-colors"
                  >
                    {restarting ? "Restarting..." : "Yes"}
                  </button>
                  <button
                    onClick={() => setConfirmRestart(false)}
                    className="text-xs bg-vsc-surface border border-vsc-border rounded px-2 py-1 text-vsc-muted hover:text-vsc-fg transition-colors"
                  >
                    No
                  </button>
                </div>
              ) : (
                <button
                  onClick={() => setConfirmRestart(true)}
                  disabled={restarting}
                  className="text-xs bg-vsc-surface border border-vsc-border rounded px-3 py-1 text-vsc-error hover:bg-vsc-header disabled:opacity-50 transition-colors"
                >
                  Restart Proxy
                </button>
              )}
            </>
          )}
          <button
            onClick={handleReload}
            disabled={reloading}
            className="text-xs bg-vsc-surface border border-vsc-border rounded px-3 py-1 text-vsc-accent hover:bg-vsc-header disabled:opacity-50 transition-colors"
          >
            {reloading ? "Reloading..." : "Reload Config"}
          </button>
        </div>
      </div>

      {restartMsg && (
        <div className="text-xs p-2 rounded border border-vsc-warning/50 text-vsc-warning bg-vsc-warning/10">
          {restartMsg}
        </div>
      )}

      {reloadResult && (
        <div
          className={`text-xs p-2 rounded border ${
            reloadResult.success
              ? "border-vsc-success/50 text-vsc-success bg-vsc-success/10"
              : "border-vsc-error/50 text-vsc-error bg-vsc-error/10"
          }`}
        >
          {reloadResult.message}
        </div>
      )}

      {error && (
        <p className="text-xs text-vsc-error">Failed to load config: {error}</p>
      )}

      {config && (
        <pre className="bg-vsc-surface border border-vsc-border rounded p-4 text-xs overflow-auto max-h-[80vh]">
          {JSON.stringify(config, null, 2)}
        </pre>
      )}
    </div>
  );
}
