import { useEffect, useRef } from "react";

export interface TimePoint {
  time: number; // Date.now() timestamp
  value: number;
}

interface LineChartProps {
  data: TimePoint[];
  label?: string;
  accentColor?: string;
}

const PADDING = { top: 16, right: 12, bottom: 28, left: 44 };

function niceStep(range: number, targetTicks: number): number {
  const rough = range / targetTicks;
  const mag = Math.pow(10, Math.floor(Math.log10(rough)));
  const norm = rough / mag;
  let step: number;
  if (norm <= 1.5) step = 1;
  else if (norm <= 3) step = 2;
  else if (norm <= 7) step = 5;
  else step = 10;
  return step * mag;
}

export default function LineChart({ data, label, accentColor }: LineChartProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    const container = containerRef.current;
    if (!canvas || !container) return;

    const rect = container.getBoundingClientRect();
    const dpr = window.devicePixelRatio || 1;
    const w = rect.width;
    const h = 140;

    canvas.width = w * dpr;
    canvas.height = h * dpr;
    canvas.style.width = `${w}px`;
    canvas.style.height = `${h}px`;

    const ctx = canvas.getContext("2d");
    if (!ctx) return;
    ctx.scale(dpr, dpr);

    // Theme colors
    const accent = accentColor || "#569cd6";
    const border = "#3c3c3c";
    const muted = "#808080";
    const bg = "#1e1e1e";

    // Clear
    ctx.fillStyle = bg;
    ctx.fillRect(0, 0, w, h);

    const plotW = w - PADDING.left - PADDING.right;
    const plotH = h - PADDING.top - PADDING.bottom;

    if (data.length < 2) {
      ctx.fillStyle = muted;
      ctx.font = "11px 'Cascadia Code', 'Fira Code', monospace";
      ctx.textAlign = "center";
      ctx.fillText("Collecting data\u2026", w / 2, h / 2);
      return;
    }

    // Compute ranges
    const first = data[0]!;
    const last = data[data.length - 1]!;
    const tMin = first.time;
    const tMax = last.time;
    const tRange = tMax - tMin || 1;

    let vMax = 0;
    for (const pt of data) {
      if (pt.value > vMax) vMax = pt.value;
    }
    vMax = vMax < 1 ? 1 : Math.ceil(vMax * 1.15); // 15% headroom

    // Y-axis grid + labels
    const yStep = niceStep(vMax, 4);
    ctx.font = "10px 'Cascadia Code', 'Fira Code', monospace";
    ctx.textAlign = "right";
    ctx.textBaseline = "middle";
    for (let v = 0; v <= vMax; v += yStep) {
      const y = PADDING.top + plotH - (v / vMax) * plotH;
      ctx.strokeStyle = border;
      ctx.lineWidth = 0.5;
      ctx.beginPath();
      ctx.moveTo(PADDING.left, y);
      ctx.lineTo(PADDING.left + plotW, y);
      ctx.stroke();

      ctx.fillStyle = muted;
      ctx.fillText(v % 1 === 0 ? v.toString() : v.toFixed(1), PADDING.left - 6, y);
    }

    // X-axis time labels
    ctx.textAlign = "center";
    ctx.textBaseline = "top";
    const elapsed = tRange / 1000;
    const steps = [0, 0.25, 0.5, 0.75, 1];
    for (const pct of steps) {
      const x = PADDING.left + pct * plotW;
      const secs = -elapsed * (1 - pct);
      let lbl: string;
      if (Math.abs(secs) >= 60) {
        lbl = `${Math.round(secs / 60)}m`;
      } else {
        lbl = `${Math.round(secs)}s`;
      }
      if (pct === 1) lbl = "now";
      ctx.fillStyle = muted;
      ctx.fillText(lbl, x, PADDING.top + plotH + 6);
    }

    // Gradient fill
    const grad = ctx.createLinearGradient(0, PADDING.top, 0, PADDING.top + plotH);
    grad.addColorStop(0, accent + "40");
    grad.addColorStop(1, accent + "00");

    // Build path points
    const points: [number, number][] = data.map((pt) => [
      PADDING.left + ((pt.time - tMin) / tRange) * plotW,
      PADDING.top + plotH - (pt.value / vMax) * plotH,
    ]);

    // Fill area
    const pFirst = points[0]!;
    const pLast = points[points.length - 1]!;
    ctx.beginPath();
    ctx.moveTo(pFirst[0], PADDING.top + plotH);
    for (const [x, y] of points) ctx.lineTo(x, y);
    ctx.lineTo(pLast[0], PADDING.top + plotH);
    ctx.closePath();
    ctx.fillStyle = grad;
    ctx.fill();

    // Draw line
    ctx.beginPath();
    ctx.moveTo(pFirst[0], pFirst[1]);
    for (let i = 1; i < points.length; i++) {
      const pt = points[i]!;
      ctx.lineTo(pt[0], pt[1]);
    }
    ctx.strokeStyle = accent;
    ctx.lineWidth = 1.5;
    ctx.lineJoin = "round";
    ctx.stroke();

    // Label
    if (label) {
      ctx.fillStyle = muted;
      ctx.font = "10px 'Cascadia Code', 'Fira Code', monospace";
      ctx.textAlign = "left";
      ctx.textBaseline = "top";
      ctx.fillText(label, PADDING.left + 4, PADDING.top + 2);
    }
  }, [data, label, accentColor]);

  // Resize observer
  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    const ro = new ResizeObserver(() => {
      // Trigger re-render by dispatching a state-free effect re-run.
      // The data dependency in the draw effect handles this naturally
      // since ResizeObserver fires and we re-draw on next frame.
      const canvas = canvasRef.current;
      if (canvas) {
        canvas.dispatchEvent(new Event("resize"));
      }
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
