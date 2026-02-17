import type { LogEntry as LogEntryType } from "../api";

const levelColors: Record<string, string> = {
  DEBUG: "text-vsc-muted",
  INFO: "text-vsc-accent",
  WARN: "text-vsc-warning",
  ERROR: "text-vsc-error",
};

interface LogEntryProps {
  entry: LogEntryType;
}

export default function LogEntry({ entry }: LogEntryProps) {
  const ts = new Date(entry.timestamp).toLocaleTimeString();
  const color = levelColors[entry.level] ?? "text-vsc-text";
  const attrs = entry.attrs && Object.keys(entry.attrs).length > 0
    ? " " + Object.entries(entry.attrs)
        .map(([k, v]) => `${k}=${JSON.stringify(v)}`)
        .join(" ")
    : "";

  return (
    <div className="flex gap-2 text-xs leading-5 hover:bg-vsc-surface/50">
      <span className="text-vsc-muted shrink-0">{ts}</span>
      <span className={`shrink-0 w-12 text-right ${color}`}>
        {entry.level}
      </span>
      <span>
        {entry.msg}
        {attrs && <span className="text-vsc-muted">{attrs}</span>}
      </span>
    </div>
  );
}
