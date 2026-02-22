import { DragEvent, useRef } from "react";

interface TopTableProps {
  title: string;
  items: { label: string; value: number }[];
  emptyText?: string;
  draggable?: boolean;
  onDragStart?: (e: DragEvent) => void;
  onDragEnd?: (e: DragEvent) => void;
  onDragOver?: (e: DragEvent) => void;
  onDrop?: (e: DragEvent) => void;
  chartVisible?: boolean;
  onToggleChart?: () => void;
  chart?: React.ReactNode;
}

export default function TopTable({
  title,
  items,
  emptyText = "No data",
  draggable,
  onDragStart,
  onDragEnd,
  onDragOver,
  onDrop,
  chartVisible,
  onToggleChart,
  chart,
}: TopTableProps) {
  const cardRef = useRef<HTMLDivElement>(null);

  return (
    <div
      ref={cardRef}
      className="bg-vsc-surface border border-vsc-border rounded p-4 transition-opacity"
      onDragOver={onDragOver}
      onDrop={onDrop}
    >
      <div className="flex items-center mb-3 gap-2">
        {draggable && (
          <span
            className="text-vsc-muted cursor-grab active:cursor-grabbing select-none text-xs leading-none"
            style={{ opacity: undefined }}
            draggable
            onDragStart={(e) => {
              if (cardRef.current) {
                e.dataTransfer.setDragImage(cardRef.current, 0, 0);
                requestAnimationFrame(() =>
                  cardRef.current?.classList.add("opacity-40"),
                );
              }
              onDragStart?.(e as unknown as DragEvent);
            }}
            onDragEnd={(e) => {
              cardRef.current?.classList.remove("opacity-40");
              onDragEnd?.(e as unknown as DragEvent);
            }}
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
      {items.length === 0 ? (
        <p className="text-xs text-vsc-muted">{emptyText}</p>
      ) : (
        <div className="max-h-64 overflow-auto">
          <table className="w-full text-xs">
            <tbody>
              {items.map((item, i) => (
                <tr
                  key={i}
                  className="border-b border-vsc-border last:border-0"
                >
                  <td className="py-1 pr-2 text-vsc-muted w-6 text-right">
                    {i + 1}
                  </td>
                  <td className="py-1 truncate max-w-0 w-full">{item.label}</td>
                  <td className="py-1 pl-2 text-right text-vsc-accent whitespace-nowrap">
                    {item.value.toLocaleString()}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {chartVisible && chart}
    </div>
  );
}
