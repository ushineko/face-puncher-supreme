export interface WSMessage {
  type: string;
  data: unknown;
}

type Listener = (data: unknown) => void;

class FPSSocket {
  private ws: WebSocket | null = null;
  private listeners = new Map<string, Set<Listener>>();
  private reconnectDelay = 1000;
  private maxDelay = 30000;
  private shouldConnect = false;
  private _connected = false;
  private onStatusChange: ((connected: boolean) => void) | null = null;

  setStatusListener(fn: (connected: boolean) => void) {
    this.onStatusChange = fn;
  }

  get connected() {
    return this._connected;
  }

  connect() {
    this.shouldConnect = true;
    this.doConnect();
  }

  disconnect() {
    this.shouldConnect = false;
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
    this.setConnected(false);
  }

  on(type: string, fn: Listener) {
    let set = this.listeners.get(type);
    if (!set) {
      set = new Set();
      this.listeners.set(type, set);
    }
    set.add(fn);
    return () => {
      set?.delete(fn);
    };
  }

  send(msg: { type: string; data?: unknown }) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(msg));
    }
  }

  private doConnect() {
    if (!this.shouldConnect) return;

    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    const url = `${proto}//${window.location.host}/fps/api/ws`;
    const ws = new WebSocket(url);

    ws.onopen = () => {
      this.reconnectDelay = 1000;
      this.setConnected(true);
    };

    ws.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data as string) as WSMessage;
        const set = this.listeners.get(msg.type);
        if (set) {
          for (const fn of set) fn(msg.data);
        }
      } catch {
        // ignore malformed messages
      }
    };

    ws.onclose = () => {
      this.ws = null;
      this.setConnected(false);
      if (this.shouldConnect) {
        setTimeout(() => this.doConnect(), this.reconnectDelay);
        this.reconnectDelay = Math.min(
          this.reconnectDelay * 2,
          this.maxDelay,
        );
      }
    };

    ws.onerror = () => {
      ws.close();
    };

    this.ws = ws;
  }

  private setConnected(v: boolean) {
    if (this._connected !== v) {
      this._connected = v;
      this.onStatusChange?.(v);
    }
  }
}

export const socket = new FPSSocket();
