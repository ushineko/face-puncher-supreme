import { useEffect } from "react";
import { Route, Routes } from "react-router-dom";
import { useAuth } from "./hooks/useAuth";
import * as api from "./api";
import { socket } from "./ws";
import Layout from "./components/Layout";
import Login from "./pages/Login";
import Stats from "./pages/Stats";
import About from "./pages/About";
import Config from "./pages/Config";
import Logs from "./pages/Logs";

export default function App() {
  const { authenticated, login, logout } = useAuth();

  // Connect WebSocket when authenticated.
  useEffect(() => {
    if (authenticated) {
      socket.setReconnectListener(() => {
        // After a reconnect (or failed reconnect attempt), verify the
        // session is still valid.  If the server restarted, in-memory
        // sessions are gone.  authStatus returns 200 {authenticated:false}
        // (not 401), so we must check the response and dispatch the
        // unauthorized event ourselves.
        void api
          .authStatus()
          .then((res) => {
            if (!res.authenticated) {
              window.dispatchEvent(new CustomEvent("fps:unauthorized"));
            }
          })
          .catch(() => {});
      });
      socket.connect();
    } else {
      socket.disconnect();
    }
    return () => {
      socket.setReconnectListener(null);
      socket.disconnect();
    };
  }, [authenticated]);

  if (authenticated === null) {
    return (
      <div className="flex items-center justify-center h-screen text-vsc-muted text-sm">
        Loading...
      </div>
    );
  }

  if (!authenticated) {
    return <Login onLogin={login} />;
  }

  return (
    <Routes>
      <Route element={<Layout onLogout={logout} />}>
        <Route index element={<Stats />} />
        <Route path="logs" element={<Logs />} />
        <Route path="config" element={<Config />} />
        <Route path="about" element={<About />} />
      </Route>
    </Routes>
  );
}
