import { useEffect, useRef } from "react";

export interface Slice {
  label: string;
  value: number;
}

interface PieChartProps {
  data: Slice[];
  maxSlices?: number;
}

const COLORS = [
  "#569cd6", // accent (blue)
  "#4ec9b0", // success (teal)
  "#dcdcaa", // warning (yellow)
  "#f44747", // error (red)
  "#c586c0", // purple
  "#ce9178", // orange
  "#9cdcfe", // light blue
  "#6a9955", // green
  "#808080", // muted (for "Other")
];

export default function PieChart({ data, maxSlices = 8 }: PieChartProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    const container = containerRef.current;
    if (!canvas || !container) return;

    const rect = container.getBoundingClientRect();
    const dpr = window.devicePixelRatio || 1;
    const w = rect.width;
    const h = 180;

    canvas.width = w * dpr;
    canvas.height = h * dpr;
    canvas.style.width = `${w}px`;
    canvas.style.height = `${h}px`;

    const ctx = canvas.getContext("2d");
    if (!ctx) return;
    ctx.scale(dpr, dpr);

    const bg = "#1e1e1e";
    const muted = "#808080";
    const textColor = "#d4d4d4";

    ctx.fillStyle = bg;
    ctx.fillRect(0, 0, w, h);

    if (data.length === 0) {
      ctx.fillStyle = muted;
      ctx.font = "11px 'Cascadia Code', 'Fira Code', monospace";
      ctx.textAlign = "center";
      ctx.fillText("No data", w / 2, h / 2);
      return;
    }

    // Aggregate slices beyond maxSlices into "Other"
    const sorted = [...data].sort((a, b) => b.value - a.value);
    let slices: Slice[];
    if (sorted.length > maxSlices) {
      slices = sorted.slice(0, maxSlices);
      const otherValue = sorted.slice(maxSlices).reduce((s, d) => s + d.value, 0);
      if (otherValue > 0) {
        slices.push({ label: "Other", value: otherValue });
      }
    } else {
      slices = sorted;
    }

    const total = slices.reduce((s, d) => s + d.value, 0);
    if (total === 0) {
      ctx.fillStyle = muted;
      ctx.font = "11px 'Cascadia Code', 'Fira Code', monospace";
      ctx.textAlign = "center";
      ctx.fillText("No data", w / 2, h / 2);
      return;
    }

    // Layout: donut on left, legend on right
    const chartSize = Math.min(h - 16, w * 0.4);
    const cx = 8 + chartSize / 2;
    const cy = h / 2;
    const outerR = chartSize / 2 - 2;
    const innerR = outerR * 0.5;

    // Draw donut
    let angle = -Math.PI / 2;
    for (let i = 0; i < slices.length; i++) {
      const s = slices[i]!;
      const sweep = (s.value / total) * Math.PI * 2;
      ctx.beginPath();
      ctx.arc(cx, cy, outerR, angle, angle + sweep);
      ctx.arc(cx, cy, innerR, angle + sweep, angle, true);
      ctx.closePath();
      ctx.fillStyle = COLORS[i % COLORS.length]!;
      ctx.fill();
      angle += sweep;
    }

    // Center hole
    ctx.beginPath();
    ctx.arc(cx, cy, innerR, 0, Math.PI * 2);
    ctx.fillStyle = bg;
    ctx.fill();

    // Legend
    const legendX = 8 + chartSize + 12;
    const legendW = w - legendX - 8;
    const lineH = 16;
    const legendStartY = Math.max(8, cy - (slices.length * lineH) / 2);

    ctx.font = "10px 'Cascadia Code', 'Fira Code', monospace";
    ctx.textBaseline = "middle";

    for (let i = 0; i < slices.length; i++) {
      const s = slices[i]!;
      const y = legendStartY + i * lineH;
      const pct = ((s.value / total) * 100).toFixed(1);

      // Color swatch
      ctx.fillStyle = COLORS[i % COLORS.length]!;
      ctx.fillRect(legendX, y - 4, 8, 8);

      // Label (truncated)
      ctx.fillStyle = textColor;
      ctx.textAlign = "left";
      const labelMaxW = legendW - 60;
      let displayLabel = s.label;
      while (ctx.measureText(displayLabel).width > labelMaxW && displayLabel.length > 3) {
        displayLabel = displayLabel.slice(0, -4) + "\u2026";
      }
      ctx.fillText(displayLabel, legendX + 14, y);

      // Percentage
      ctx.fillStyle = muted;
      ctx.textAlign = "right";
      ctx.fillText(`${pct}%`, w - 8, y);
    }
  }, [data, maxSlices]);

  // Resize observer
  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    const ro = new ResizeObserver(() => {
      const canvas = canvasRef.current;
      if (canvas) canvas.dispatchEvent(new Event("resize"));
    });
    ro.observe(container);
    return () => ro.disconnect();
  }, []);

  return (
    <div ref={containerRef} className="w-full mt-2">
      <canvas ref={canvasRef} />
    </div>
  );
}
