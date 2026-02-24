import { useRef, useCallback, useMemo, useState, useEffect } from "react";
import { useSocket } from "../hooks/useSocket";
import { useLayout } from "../hooks/useLayout";
import StatCard, { StatRow } from "../components/StatCard";
import TopTable from "../components/TopTable";
import LineChart, { TimePoint } from "../components/LineChart";
import PieChart from "../components/PieChart";

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

interface ResourcesData {
  mem_alloc_mb: number;
  mem_sys_mb: number;
  mem_heap_inuse_mb: number;
  goroutines: number;
  open_fds: number;
  max_fds: number;
}

interface WatermarksData {
  peak_req_per_sec: number;
  peak_bytes_in_sec: number;
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
  resources: ResourcesData;
  watermarks: WatermarksData;
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

const MAX_HISTORY = 60; // ~3 minutes at 3s intervals

export default function Stats() {
  const heartbeat = useSocket<HeartbeatData>("heartbeat");
  const stats = useSocket<StatsData>("stats");
  const reqRate = useRate(stats?.traffic.total_requests ?? 0);
  const bytesInRate = useRate(stats?.traffic.total_bytes_in ?? 0);

  const layout = useLayout();

  // Rolling time-series for the traffic line chart.
  // Must use useState (not useRef) so LineChart receives a new array reference
  // on each update — otherwise the canvas draw effect never re-fires.
  const [trafficHistory, setTrafficHistory] = useState<TimePoint[]>([]);
  useEffect(() => {
    if (!stats) return;
    setTrafficHistory((prev) => {
      const rate = parseFloat(reqRate);
      const next = [...prev, { time: Date.now(), value: rate }];
      return next.length > MAX_HISTORY
        ? next.slice(next.length - MAX_HISTORY)
        : next;
    });
  }, [stats, reqRate]);

  // Build card renderers keyed by section ID
  const cardRenderers: Record<string, () => React.ReactNode> = useMemo(
    () => ({
      server: () =>
        heartbeat ? (
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
            {stats && (
              <div className="mt-2 border-t border-vsc-border pt-2">
                <div className="text-xs text-vsc-accent mb-1">Resources</div>
                <StatRow
                  label="Goroutines"
                  value={stats.resources.goroutines.toLocaleString()}
                />
                <StatRow
                  label="Heap"
                  value={`${stats.resources.mem_heap_inuse_mb.toFixed(1)} MB`}
                />
                <StatRow
                  label="Memory (OS)"
                  value={`${stats.resources.mem_sys_mb.toFixed(1)} MB`}
                />
                <StatRow
                  label="Open FDs"
                  value={
                    stats.resources.open_fds === -1
                      ? "N/A"
                      : `${stats.resources.open_fds} / ${stats.resources.max_fds === -1 ? "?" : stats.resources.max_fds}`
                  }
                />
              </div>
            )}
          </>
        ) : (
          <p className="text-xs text-vsc-muted">Waiting...</p>
        ),
      filtering: () =>
        stats ? (
          <>
            <div className="text-xs text-vsc-accent mb-1">Connections</div>
            <StatRow
              label="Total"
              value={stats.connections.total.toLocaleString()}
              accent
            />
            <StatRow
              label="Active"
              value={stats.connections.active.toLocaleString()}
            />
            <div className="mt-2 border-t border-vsc-border pt-2">
              <div className="text-xs text-vsc-accent mb-1">Blocking</div>
              <StatRow
                label="Blocked"
                value={stats.blocking.blocks_total.toLocaleString()}
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
            </div>
            {stats.mitm.enabled && (
              <div className="mt-2 border-t border-vsc-border pt-2">
                <div className="text-xs text-vsc-accent mb-1">MITM</div>
                <StatRow
                  label="Intercepts"
                  value={stats.mitm.intercepts_total.toLocaleString()}
                />
                <StatRow
                  label="Domains"
                  value={stats.mitm.domains_configured.toString()}
                />
              </div>
            )}
          </>
        ) : (
          <p className="text-xs text-vsc-muted">Waiting...</p>
        ),
      traffic: () =>
        stats ? (
          <>
            <StatRow
              label="Requests"
              value={stats.traffic.total_requests.toLocaleString()}
              accent
            />
            <StatRow label="Req/sec" value={reqRate} />
            <StatRow
              label="Peak Req/sec"
              value={stats.watermarks.peak_req_per_sec.toFixed(1)}
            />
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
            <StatRow
              label="In/sec"
              value={formatBytes(Number(bytesInRate))}
            />
            <StatRow
              label="Peak In/sec"
              value={formatBytes(stats.watermarks.peak_bytes_in_sec)}
            />
          </>
        ) : (
          <p className="text-xs text-vsc-muted">Waiting...</p>
        ),
      plugins: () =>
        stats && stats.plugins.active > 0 ? (
          <>
            <StatRow label="Active" value={stats.plugins.active.toString()} />
            {stats.plugins.filters.map((f) => (
              <div
                key={f.name}
                className="mt-2 border-t border-vsc-border pt-2"
              >
                <div className="text-xs text-vsc-accent">
                  {f.name}@{f.version}
                </div>
                <StatRow
                  label="Inspected"
                  value={f.responses_inspected.toLocaleString()}
                />
                <StatRow
                  label="Matched"
                  value={f.responses_matched.toLocaleString()}
                />
                <StatRow
                  label="Modified"
                  value={f.responses_modified.toLocaleString()}
                />
              </div>
            ))}
          </>
        ) : null,
    }),
    [heartbeat, stats, reqRate, bytesInRate],
  );

  const cardTitles: Record<string, string> = {
    server: "Server",
    filtering: "Filtering",
    traffic: "Traffic",
    plugins: "Plugins",
  };

  // Charts for stat cards
  const cardCharts: Record<string, React.ReactNode> = {
    traffic: (
      <LineChart
        data={trafficHistory}
        label="req/sec"
      />
    ),
  };

  // Determine which cards are available (have content to show)
  const availableCards = Object.keys(cardRenderers).filter((id) => {
    if (id === "plugins") return stats && stats.plugins.active > 0;
    return true;
  });

  const orderedCards = layout.getCardOrder(availableCards);

  // Build table definitions
  const buildTableDefs = useCallback(() => {
    if (!stats) return [];
    const tables: {
      id: string;
      title: string;
      items: { label: string; value: number }[];
      chartData?: { label: string; value: number }[];
    }[] = [
      {
        id: "top-blocked",
        title: "Top Blocked Domains",
        items: stats.blocking.top_blocked.map((e) => ({
          label: e.domain,
          value: e.count,
        })),
      },
      {
        id: "top-allowed",
        title: "Top Allowed Domains",
        items: stats.blocking.top_allowed.map((e) => ({
          label: e.domain,
          value: e.count,
        })),
      },
      {
        id: "top-requested",
        title: "Top Requested Domains",
        items: stats.domains.top_requested.map((e) => ({
          label: e.domain,
          value: e.count,
        })),
      },
      {
        id: "top-clients",
        title: "Top Clients",
        items: stats.clients.top_by_requests.map((e) => ({
          label: e.hostname || e.client_ip,
          value: e.requests,
        })),
      },
    ];

    if (stats.mitm.enabled && stats.mitm.top_intercepted.length > 0) {
      tables.push({
        id: "top-intercepted",
        title: "Top Intercepted Domains",
        items: stats.mitm.top_intercepted.map((e) => ({
          label: e.domain,
          value: e.count,
        })),
      });
    }

    for (const f of stats.plugins.filters) {
      if (f.top_rules.length > 0) {
        tables.push({
          id: `top-plugin-rules-${f.name}`,
          title: `Top Rules: ${f.name}`,
          items: f.top_rules.map((r) => ({
            label: r.rule,
            value: r.count,
          })),
        });
      }
    }

    return tables;
  }, [stats]);

  const tableDefs = buildTableDefs();
  const availableTables = tableDefs.map((t) => t.id);
  const orderedTables = layout.getTableOrder(availableTables);

  // IDs that get pie charts
  const pieChartIds = new Set([
    "top-blocked",
    "top-requested",
    "top-clients",
  ]);

  return (
    <div className="space-y-4">
      {/* Header with reset button */}
      <div className="flex justify-end">
        <button
          onClick={layout.resetLayout}
          className="text-xs text-vsc-muted hover:text-vsc-accent transition-colors"
        >
          Reset Layout
        </button>
      </div>

      {/* Stat Cards */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
        {orderedCards.map((id) => {
          const content = cardRenderers[id]?.();
          if (content === null || content === undefined) return null;
          const hasChart = id in cardCharts;
          return (
            <StatCard
              key={id}
              title={cardTitles[id] || id}
              sectionId={id}
              draggable
              onDragStart={layout.onDragStart("card", id)}
              onDragEnd={layout.onDragEnd}
              onDragOver={layout.onDragOver}
              onDrop={layout.onDrop("card", id)}
              chartVisible={hasChart ? layout.isChartVisible(id) : undefined}
              onToggleChart={hasChart ? () => layout.toggleChart(id) : undefined}
              chart={hasChart ? cardCharts[id] : undefined}
            >
              {content}
            </StatCard>
          );
        })}
      </div>

      {/* Top-N Tables */}
      {stats && (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {orderedTables.map((id) => {
            const def = tableDefs.find((t) => t.id === id);
            if (!def) return null;
            const hasPie = pieChartIds.has(id);
            return (
              <TopTable
                key={id}
                title={def.title}
                items={def.items}
                draggable
                onDragStart={layout.onDragStart("table", id)}
                onDragEnd={layout.onDragEnd}
                onDragOver={layout.onDragOver}
                onDrop={layout.onDrop("table", id)}
                chartVisible={
                  hasPie ? layout.isChartVisible(id) : undefined
                }
                onToggleChart={
                  hasPie ? () => layout.toggleChart(id) : undefined
                }
                chart={
                  hasPie ? (
                    <PieChart data={def.items} />
                  ) : undefined
                }
              />
            );
          })}
        </div>
      )}
    </div>
  );
}
