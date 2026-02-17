import { useEffect } from "react";
import { Route, Routes } from "react-router-dom";
import { useAuth } from "./hooks/useAuth";
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
      socket.connect();
    } else {
      socket.disconnect();
    }
    return () => socket.disconnect();
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
