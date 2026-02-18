import { useCallback, useEffect, useRef, useState } from "react";
import * as api from "../api";

export function useAuth() {
  const [authenticated, setAuthenticated] = useState<boolean | null>(null);
  const tokenRef = useRef<string | null>(null);

  const checkAuth = useCallback(async () => {
    try {
      const res = await api.authStatus();
      setAuthenticated(res.authenticated);
      if (!res.authenticated) {
        tokenRef.current = null;
      }
    } catch {
      setAuthenticated(false);
      tokenRef.current = null;
    }
  }, []);

  useEffect(() => {
    void checkAuth();
    const handler = () => {
      setAuthenticated(false);
      tokenRef.current = null;
    };
    window.addEventListener("fps:unauthorized", handler);
    return () => window.removeEventListener("fps:unauthorized", handler);
  }, [checkAuth]);

  const doLogin = useCallback(
    async (username: string, password: string) => {
      const token = await api.login(username, password);
      tokenRef.current = token;
      setAuthenticated(true);
    },
    [],
  );

  const doLogout = useCallback(async () => {
    await api.logout();
    tokenRef.current = null;
    setAuthenticated(false);
  }, []);

  return { authenticated, token: tokenRef, login: doLogin, logout: doLogout };
}
