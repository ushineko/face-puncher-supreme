import { NavLink, Outlet } from "react-router-dom";
import ReconnectBanner from "./ReconnectBanner";

const navItems = [
  { to: "/", label: "Stats" },
  { to: "/logs", label: "Logs" },
  { to: "/config", label: "Config" },
  { to: "/about", label: "About" },
];

interface LayoutProps {
  onLogout: () => void;
}

export default function Layout({ onLogout }: LayoutProps) {
  return (
    <div className="flex h-screen flex-col">
      <header className="flex items-center justify-between bg-vsc-header px-4 py-2 border-b border-vsc-border">
        <div className="flex items-center gap-6">
          <span className="text-vsc-accent font-bold text-sm">FPS</span>
          <nav className="flex gap-4">
            {navItems.map((item) => (
              <NavLink
                key={item.to}
                to={item.to}
                end={item.to === "/"}
                className={({ isActive }) =>
                  `text-sm px-2 py-1 rounded transition-colors ${
                    isActive
                      ? "text-vsc-accent bg-vsc-surface"
                      : "text-vsc-muted hover:text-vsc-text"
                  }`
                }
              >
                {item.label}
              </NavLink>
            ))}
          </nav>
        </div>
        <button
          onClick={onLogout}
          className="text-xs text-vsc-muted hover:text-vsc-error transition-colors"
        >
          Logout
        </button>
      </header>
      <ReconnectBanner />
      <main className="flex-1 overflow-auto p-4">
        <Outlet />
      </main>
    </div>
  );
}
