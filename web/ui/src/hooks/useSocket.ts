import { useEffect, useState } from "react";
import { socket } from "../ws";

export function useSocket<T>(type: string): T | null {
  const [data, setData] = useState<T | null>(null);

  useEffect(() => {
    return socket.on(type, (d) => setData(d as T));
  }, [type]);

  return data;
}

export function useSocketConnected(): boolean {
  const [connected, setConnected] = useState(socket.connected);

  useEffect(() => {
    socket.setStatusListener(setConnected);
    return () => socket.setStatusListener(() => {});
  }, []);

  return connected;
}
