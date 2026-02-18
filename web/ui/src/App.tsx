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
  const { authenticated, token, login, logout } = useAuth();

  // Connect WebSocket when authenticated.
  useEffect(() => {
    if (authenticated) {
      socket.setTokenFn(() => token.current);
      socket.setReconnectListener(() => {
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
      socket.setTokenFn(null);
      socket.disconnect();
    };
  }, [authenticated, token]);

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
