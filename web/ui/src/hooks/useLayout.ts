import { useState, useCallback, useRef, DragEvent } from "react";

const STORAGE_KEY = "fps-dashboard-layout";

const DEFAULT_CARD_ORDER = [
  "server",
  "connections",
  "traffic",
  "blocking",
  "mitm",
  "plugins",
  "resources",
];

const DEFAULT_TABLE_ORDER = [
  "top-blocked",
  "top-allowed",
  "top-requested",
  "top-clients",
  "top-intercepted",
  "top-plugin-rules",
];

interface DashboardLayout {
  cardOrder: string[];
  tableOrder: string[];
  chartsVisible: Record<string, boolean>;
}

function loadLayout(): DashboardLayout {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as Partial<DashboardLayout>;
      return {
        cardOrder: Array.isArray(parsed.cardOrder)
          ? parsed.cardOrder
          : DEFAULT_CARD_ORDER,
        tableOrder: Array.isArray(parsed.tableOrder)
          ? parsed.tableOrder
          : DEFAULT_TABLE_ORDER,
        chartsVisible:
          parsed.chartsVisible && typeof parsed.chartsVisible === "object"
            ? parsed.chartsVisible
            : {},
      };
    }
  } catch {
    // Corrupt data — fall through to defaults
  }
  return {
    cardOrder: [...DEFAULT_CARD_ORDER],
    tableOrder: [...DEFAULT_TABLE_ORDER],
    chartsVisible: {},
  };
}

function saveLayout(layout: DashboardLayout) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(layout));
  } catch {
    // Storage full or unavailable — silently ignore
  }
}

/** Ensure all `available` IDs appear in `order`, appending any missing ones. */
function reconcileOrder(order: string[], available: string[]): string[] {
  const result = order.filter((id) => available.includes(id));
  for (const id of available) {
    if (!result.includes(id)) result.push(id);
  }
  return result;
}

export function useLayout() {
  const [layout, setLayout] = useState<DashboardLayout>(loadLayout);
  const dragItemRef = useRef<{ group: "card" | "table"; id: string } | null>(
    null,
  );

  const updateLayout = useCallback((updater: (prev: DashboardLayout) => DashboardLayout) => {
    setLayout((prev) => {
      const next = updater(prev);
      saveLayout(next);
      return next;
    });
  }, []);

  const getCardOrder = useCallback(
    (available: string[]) => reconcileOrder(layout.cardOrder, available),
    [layout.cardOrder],
  );

  const getTableOrder = useCallback(
    (available: string[]) => reconcileOrder(layout.tableOrder, available),
    [layout.tableOrder],
  );

  const isChartVisible = useCallback(
    (id: string) => layout.chartsVisible[id] ?? false,
    [layout.chartsVisible],
  );

  const toggleChart = useCallback(
    (id: string) => {
      updateLayout((prev) => ({
        ...prev,
        chartsVisible: {
          ...prev.chartsVisible,
          [id]: !prev.chartsVisible[id],
        },
      }));
    },
    [updateLayout],
  );

  const resetLayout = useCallback(() => {
    const fresh: DashboardLayout = {
      cardOrder: [...DEFAULT_CARD_ORDER],
      tableOrder: [...DEFAULT_TABLE_ORDER],
      chartsVisible: {},
    };
    saveLayout(fresh);
    setLayout(fresh);
  }, []);

  // Drag and drop handlers
  const onDragStart = useCallback(
    (group: "card" | "table", id: string) => (e: DragEvent) => {
      dragItemRef.current = { group, id };
      e.dataTransfer.effectAllowed = "move";
      // Slight delay so the browser captures the drag image first
      const el = e.currentTarget as HTMLElement;
      requestAnimationFrame(() => el.classList.add("opacity-40"));
    },
    [],
  );

  const onDragEnd = useCallback((e: DragEvent) => {
    dragItemRef.current = null;
    (e.currentTarget as HTMLElement).classList.remove("opacity-40");
  }, []);

  const onDragOver = useCallback((e: DragEvent) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
  }, []);

  const onDrop = useCallback(
    (group: "card" | "table", targetId: string) => (e: DragEvent) => {
      e.preventDefault();
      const drag = dragItemRef.current;
      if (!drag || drag.group !== group || drag.id === targetId) return;

      updateLayout((prev) => {
        const key = group === "card" ? "cardOrder" : "tableOrder";
        const order = [...prev[key]];
        const fromIdx = order.indexOf(drag.id);
        const toIdx = order.indexOf(targetId);
        if (fromIdx === -1 || toIdx === -1) return prev;
        order.splice(fromIdx, 1);
        order.splice(toIdx, 0, drag.id);
        return { ...prev, [key]: order };
      });
    },
    [updateLayout],
  );

  return {
    getCardOrder,
    getTableOrder,
    isChartVisible,
    toggleChart,
    resetLayout,
    onDragStart,
    onDragEnd,
    onDragOver,
    onDrop,
  };
}
