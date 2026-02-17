import { useSocketConnected } from "../hooks/useSocket";

export default function ReconnectBanner() {
  const connected = useSocketConnected();

  if (connected) return null;

  return (
    <div className="bg-vsc-error/20 border-b border-vsc-error text-vsc-error text-xs text-center py-1">
      Reconnecting to server...
    </div>
  );
}
