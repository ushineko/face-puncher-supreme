import { DragEvent } from "react";

interface StatCardProps {
  title: string;
  children: React.ReactNode;
  sectionId?: string;
  draggable?: boolean;
  onDragStart?: (e: DragEvent) => void;
  onDragEnd?: (e: DragEvent) => void;
  onDragOver?: (e: DragEvent) => void;
  onDrop?: (e: DragEvent) => void;
  chartVisible?: boolean;
  onToggleChart?: () => void;
  chart?: React.ReactNode;
}

export default function StatCard({
  title,
  children,
  draggable,
  onDragStart,
  onDragEnd,
  onDragOver,
  onDrop,
  chartVisible,
  onToggleChart,
  chart,
}: StatCardProps) {
  return (
    <div
      className="bg-vsc-surface border border-vsc-border rounded p-4 transition-opacity"
      draggable={draggable}
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      onDragOver={onDragOver}
      onDrop={onDrop}
    >
      <div className="flex items-center mb-3 gap-2">
        {draggable && (
          <span className="text-vsc-muted opacity-0 group-hover:opacity-100 hover:opacity-100 cursor-grab active:cursor-grabbing select-none text-xs leading-none"
            style={{ opacity: undefined }}
            onMouseEnter={(e) => (e.currentTarget.style.opacity = "1")}
            onMouseLeave={(e) => (e.currentTarget.style.opacity = "")}
          >
            ⠿
          </span>
        )}
        <h3 className="text-xs text-vsc-muted uppercase tracking-wider flex-1">
          {title}
        </h3>
        {onToggleChart && (
          <button
            onClick={onToggleChart}
            className={`text-xs px-1 rounded transition-colors ${
              chartVisible
                ? "text-vsc-accent bg-vsc-bg"
                : "text-vsc-muted hover:text-vsc-accent"
            }`}
            title={chartVisible ? "Hide chart" : "Show chart"}
          >
            ▤
          </button>
        )}
      </div>
      {children}
      {chartVisible && chart}
    </div>
  );
}

interface StatRowProps {
  label: string;
  value: string | number;
  accent?: boolean;
}

export function StatRow({ label, value, accent }: StatRowProps) {
  return (
    <div className="flex justify-between text-sm py-0.5">
      <span className="text-vsc-muted">{label}</span>
      <span className={accent ? "text-vsc-accent" : ""}>{value}</span>
    </div>
  );
}
