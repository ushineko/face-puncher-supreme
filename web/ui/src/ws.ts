export interface WSMessage {
  type: string;
  data: unknown;
}

type Listener = (data: unknown) => void;

let wsSeq = 0;

class FPSSocket {
  private ws: WebSocket | null = null;
  private listeners = new Map<string, Set<Listener>>();
  private reconnectDelay = 1000;
  private maxDelay = 30000;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;
  private shouldConnect = false;
  private _connected = false;
  private hasConnectedOnce = false;
  private onStatusChange: ((connected: boolean) => void) | null = null;
  private onReconnectFn: (() => void) | null = null;
  private tokenFn: (() => string | null) | null = null;

  setStatusListener(fn: (connected: boolean) => void) {
    this.onStatusChange = fn;
  }

  setReconnectListener(fn: (() => void) | null) {
    this.onReconnectFn = fn;
  }

  setTokenFn(fn: (() => string | null) | null) {
    this.tokenFn = fn;
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
    this.hasConnectedOnce = false;
    this.reconnectDelay = 1000;
    if (this.retryTimer !== null) {
      clearTimeout(this.retryTimer);
      this.retryTimer = null;
    }
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

    const id = ++wsSeq;
    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    let url = `${proto}//${window.location.host}/fps/api/ws`;

    // Append session token as query param — needed when the browser routes
    // WebSocket through a CONNECT tunnel (proxy config) where cookies set
    // on direct HTTP connections may not be forwarded.
    const token = this.tokenFn?.();
    if (token) {
      url += `?token=${encodeURIComponent(token)}`;
    }

    const ws = new WebSocket(url);

    ws.onopen = () => {
      if (this.ws !== ws) return;
      this.reconnectDelay = 1000;
      const isReconnect = this.hasConnectedOnce;
      this.hasConnectedOnce = true;
      this.setConnected(true);
      if (isReconnect) {
        this.onReconnectFn?.();
      }
    };

    ws.onmessage = (ev) => {
      if (this.ws !== ws) return;
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

    ws.onclose = (ev) => {
      if (this.ws !== ws) return;
      this.ws = null;
      this.setConnected(false);
      if (this.hasConnectedOnce && this.shouldConnect) {
        this.onReconnectFn?.();
      }
      if (this.shouldConnect) {
        this.retryTimer = setTimeout(() => {
          this.retryTimer = null;
          this.doConnect();
        }, this.reconnectDelay);
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
