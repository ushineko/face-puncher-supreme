import { useRef } from "react";
import { useSocket } from "../hooks/useSocket";
import StatCard, { StatRow } from "../components/StatCard";
import TopTable from "../components/TopTable";

interface HeartbeatData {
  status: string;
  version: string;
  mode: string;
  mitm_enabled: boolean;
  mitm_domains: number;
  plugins_active: number;
  plugins: string[];
  uptime_seconds: number;
  os: string;
  arch: string;
  go_version: string;
  started_at: string;
}

interface TopEntry {
  domain: string;
  count: number;
}

interface ClientEntry {
  client_ip: string;
  hostname?: string;
  requests: number;
  blocked: number;
  bytes_in: number;
  bytes_out: number;
}

interface PluginFilterEntry {
  name: string;
  version: string;
  mode: string;
  responses_inspected: number;
  responses_matched: number;
  responses_modified: number;
  top_rules: { rule: string; count: number }[];
}

interface StatsData {
  connections: { total: number; active: number };
  blocking: {
    blocks_total: number;
    allows_total: number;
    blocklist_size: number;
    allowlist_size: number;
    blocklist_sources: number;
    top_blocked: TopEntry[];
    top_allowed: TopEntry[];
  };
  mitm: {
    enabled: boolean;
    intercepts_total: number;
    domains_configured: number;
    top_intercepted: TopEntry[];
  };
  plugins: {
    active: number;
    filters: PluginFilterEntry[];
  };
  domains: { top_requested: TopEntry[] };
  clients: { top_by_requests: ClientEntry[] };
  traffic: {
    total_requests: number;
    total_blocked: number;
    total_bytes_in: number;
    total_bytes_out: number;
  };
}

function formatUptime(seconds: number): string {
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return `${d}d ${h}h ${m}m`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024)
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

function useRate(current: number): string {
  const prevRef = useRef<{ value: number; time: number } | null>(null);
  const rateRef = useRef(0);

  const now = Date.now();
  if (prevRef.current) {
    const dt = (now - prevRef.current.time) / 1000;
    if (dt > 0) {
      rateRef.current = (current - prevRef.current.value) / dt;
    }
  }
  prevRef.current = { value: current, time: now };
  return rateRef.current > 0 ? rateRef.current.toFixed(1) : "0";
}

export default function Stats() {
  const heartbeat = useSocket<HeartbeatData>("heartbeat");
  const stats = useSocket<StatsData>("stats");
  const reqRate = useRate(stats?.traffic.total_requests ?? 0);
  const bytesInRate = useRate(stats?.traffic.total_bytes_in ?? 0);

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
        {/* Heartbeat */}
        <StatCard title="Server">
          {heartbeat ? (
            <>
              <StatRow label="Version" value={heartbeat.version} accent />
              <StatRow
                label="Uptime"
                value={formatUptime(heartbeat.uptime_seconds)}
              />
              <StatRow label="Mode" value={heartbeat.mode} />
              <StatRow
                label="MITM"
                value={
                  heartbeat.mitm_enabled
                    ? `${heartbeat.mitm_domains} domains`
                    : "off"
                }
              />
              <StatRow
                label="Plugins"
                value={heartbeat.plugins_active.toString()}
              />
              <StatRow
                label="Platform"
                value={`${heartbeat.os}/${heartbeat.arch}`}
              />
              <StatRow label="Go" value={heartbeat.go_version} />
            </>
          ) : (
            <p className="text-xs text-vsc-muted">Waiting...</p>
          )}
        </StatCard>

        {/* Connections */}
        <StatCard title="Connections">
          {stats ? (
            <>
              <StatRow
                label="Total"
                value={stats.connections.total.toLocaleString()}
                accent
              />
              <StatRow
                label="Active"
                value={stats.connections.active.toLocaleString()}
              />
            </>
          ) : (
            <p className="text-xs text-vsc-muted">Waiting...</p>
          )}
        </StatCard>

        {/* Traffic */}
        <StatCard title="Traffic">
          {stats ? (
            <>
              <StatRow
                label="Requests"
                value={stats.traffic.total_requests.toLocaleString()}
                accent
              />
              <StatRow label="Req/sec" value={reqRate} />
              <StatRow
                label="Blocked"
                value={stats.traffic.total_blocked.toLocaleString()}
              />
              <StatRow
                label="Bytes In"
                value={formatBytes(stats.traffic.total_bytes_in)}
              />
              <StatRow
                label="Bytes Out"
                value={formatBytes(stats.traffic.total_bytes_out)}
              />
              <StatRow label="In/sec" value={formatBytes(Number(bytesInRate))} />
            </>
          ) : (
            <p className="text-xs text-vsc-muted">Waiting...</p>
          )}
        </StatCard>

        {/* Blocking */}
        <StatCard title="Blocking">
          {stats ? (
            <>
              <StatRow
                label="Blocked"
                value={stats.blocking.blocks_total.toLocaleString()}
                accent
              />
              <StatRow
                label="Allowed"
                value={stats.blocking.allows_total.toLocaleString()}
              />
              <StatRow
                label="Blocklist"
                value={`${stats.blocking.blocklist_size.toLocaleString()} domains`}
              />
              <StatRow
                label="Allowlist"
                value={`${stats.blocking.allowlist_size.toLocaleString()} entries`}
              />
              <StatRow
                label="Sources"
                value={stats.blocking.blocklist_sources.toString()}
              />
            </>
          ) : (
            <p className="text-xs text-vsc-muted">Waiting...</p>
          )}
        </StatCard>

        {/* MITM */}
        {stats?.mitm.enabled && (
          <StatCard title="MITM Interception">
            <StatRow
              label="Intercepts"
              value={stats.mitm.intercepts_total.toLocaleString()}
              accent
            />
            <StatRow
              label="Domains"
              value={stats.mitm.domains_configured.toString()}
            />
          </StatCard>
        )}

        {/* Plugins */}
        {stats && stats.plugins.active > 0 && (
          <StatCard title="Plugins">
            <StatRow label="Active" value={stats.plugins.active.toString()} />
            {stats.plugins.filters.map((f) => (
              <div key={f.name} className="mt-2 border-t border-vsc-border pt-2">
                <div className="text-xs text-vsc-accent">{f.name}@{f.version}</div>
                <StatRow label="Inspected" value={f.responses_inspected.toLocaleString()} />
                <StatRow label="Matched" value={f.responses_matched.toLocaleString()} />
                <StatRow label="Modified" value={f.responses_modified.toLocaleString()} />
              </div>
            ))}
          </StatCard>
        )}
      </div>

      {/* Top-N Tables */}
      {stats && (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <TopTable
            title="Top Blocked Domains"
            items={stats.blocking.top_blocked.map((e) => ({
              label: e.domain,
              value: e.count,
            }))}
          />
          <TopTable
            title="Top Allowed Domains"
            items={stats.blocking.top_allowed.map((e) => ({
              label: e.domain,
              value: e.count,
            }))}
          />
          <TopTable
            title="Top Requested Domains"
            items={stats.domains.top_requested.map((e) => ({
              label: e.domain,
              value: e.count,
            }))}
          />
          <TopTable
            title="Top Clients"
            items={stats.clients.top_by_requests.map((e) => ({
              label: e.hostname || e.client_ip,
              value: e.requests,
            }))}
          />
          {stats.mitm.enabled && stats.mitm.top_intercepted.length > 0 && (
            <TopTable
              title="Top Intercepted Domains"
              items={stats.mitm.top_intercepted.map((e) => ({
                label: e.domain,
                value: e.count,
              }))}
            />
          )}
          {stats.plugins.filters.map((f) =>
            f.top_rules.length > 0 ? (
              <TopTable
                key={f.name}
                title={`Top Rules: ${f.name}`}
                items={f.top_rules.map((r) => ({
                  label: r.rule,
                  value: r.count,
                }))}
              />
            ) : null,
          )}
        </div>
      )}
    </div>
  );
}
