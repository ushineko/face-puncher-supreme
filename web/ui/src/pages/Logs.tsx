import { useCallback, useEffect, useRef, useState } from "react";
import { fetchLogs, type LogEntry as LogEntryType } from "../api";
import { socket } from "../ws";
import LogEntry from "../components/LogEntry";

const levels = ["DEBUG", "INFO", "WARN", "ERROR"] as const;

export default function Logs() {
  const [entries, setEntries] = useState<LogEntryType[]>([]);
  const [level, setLevel] = useState<string>("INFO");
  const [search, setSearch] = useState("");
  const [paused, setPaused] = useState(false);
  const bottomRef = useRef<HTMLDivElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  // Backfill on mount.
  useEffect(() => {
    fetchLogs(500, level)
      .then(setEntries)
      .catch(() => {});
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // Subscribe to log stream.
  useEffect(() => {
    return socket.on("log", (data) => {
      const entry = data as LogEntryType;
      setEntries((prev) => {
        const next = [...prev, entry];
        // Keep max 2000 entries in UI buffer.
        return next.length > 2000 ? next.slice(-2000) : next;
      });
    });
  }, []);

  // Update server-side level filter.
  const changeLevel = useCallback((newLevel: string) => {
    setLevel(newLevel);
    socket.send({ type: "set_log_level", data: { min_level: newLevel } });
  }, []);

  // Auto-scroll.
  useEffect(() => {
    if (!paused) {
      bottomRef.current?.scrollIntoView({ behavior: "smooth" });
    }
  }, [entries, paused]);

  const filtered = search
    ? entries.filter(
        (e) =>
          e.msg.toLowerCase().includes(search.toLowerCase()) ||
          JSON.stringify(e.attrs ?? {})
            .toLowerCase()
            .includes(search.toLowerCase()),
      )
    : entries;

  return (
    <div className="flex flex-col h-full">
      {/* Controls */}
      <div className="flex items-center gap-3 mb-2 shrink-0">
        <select
          value={level}
          onChange={(e) => changeLevel(e.target.value)}
          className="bg-vsc-surface border border-vsc-border rounded px-2 py-1 text-xs text-vsc-text outline-none"
        >
          {levels.map((l) => (
            <option key={l} value={l}>
              {l}
            </option>
          ))}
        </select>
        <input
          type="text"
          placeholder="Search logs..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="bg-vsc-surface border border-vsc-border rounded px-2 py-1 text-xs text-vsc-text outline-none flex-1 max-w-xs focus:border-vsc-accent"
        />
        <button
          onClick={() => setPaused((p) => !p)}
          className={`text-xs px-2 py-1 rounded border ${
            paused
              ? "border-vsc-warning text-vsc-warning"
              : "border-vsc-border text-vsc-muted"
          }`}
        >
          {paused ? "Resume" : "Pause"}
        </button>
        <span className="text-xs text-vsc-muted">
          {filtered.length} entries
        </span>
      </div>

      {/* Log container */}
      <div
        ref={containerRef}
        className="flex-1 overflow-auto bg-vsc-surface border border-vsc-border rounded p-2 font-mono"
      >
        {filtered.map((entry, i) => (
          <LogEntry key={i} entry={entry} />
        ))}
        <div ref={bottomRef} />
      </div>
    </div>
  );
}
