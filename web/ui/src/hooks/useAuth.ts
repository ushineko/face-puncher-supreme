import { useCallback, useEffect, useState } from "react";
import * as api from "../api";

export function useAuth() {
  const [authenticated, setAuthenticated] = useState<boolean | null>(null);

  const checkAuth = useCallback(async () => {
    try {
      const res = await api.authStatus();
      setAuthenticated(res.authenticated);
    } catch {
      setAuthenticated(false);
    }
  }, []);

  useEffect(() => {
    void checkAuth();
    const handler = () => setAuthenticated(false);
    window.addEventListener("fps:unauthorized", handler);
    return () => window.removeEventListener("fps:unauthorized", handler);
  }, [checkAuth]);

  const doLogin = useCallback(
    async (username: string, password: string) => {
      await api.login(username, password);
      setAuthenticated(true);
    },
    [],
  );

  const doLogout = useCallback(async () => {
    await api.logout();
    setAuthenticated(false);
  }, []);

  return { authenticated, login: doLogin, logout: doLogout };
}
