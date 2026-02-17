import { useCallback, useEffect, useState } from "react";
import { fetchConfig } from "../api";
import { socket } from "../ws";
import { useSocket } from "../hooks/useSocket";

interface ReloadResult {
  success: boolean;
  message: string;
}

export default function Config() {
  const [config, setConfig] = useState<Record<string, unknown> | null>(null);
  const [error, setError] = useState("");
  const [reloading, setReloading] = useState(false);
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

  // Auto-refresh config after successful reload.
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

  return (
    <div className="max-w-4xl space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm text-vsc-accent font-bold">
          Proxy Configuration
        </h2>
        <button
          onClick={handleReload}
          disabled={reloading}
          className="text-xs bg-vsc-surface border border-vsc-border rounded px-3 py-1 text-vsc-accent hover:bg-vsc-header disabled:opacity-50 transition-colors"
        >
          {reloading ? "Reloading..." : "Reload Config"}
        </button>
      </div>

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
