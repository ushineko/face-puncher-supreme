import { FormEvent, useState } from "react";

interface LoginProps {
  onLogin: (username: string, password: string) => Promise<void>;
}

export default function Login({ onLogin }: LoginProps) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setError("");
    setLoading(true);
    try {
      await onLogin(username, password);
    } catch {
      setError("Invalid credentials");
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="flex items-center justify-center h-screen bg-vsc-bg">
      <form
        onSubmit={handleSubmit}
        className="bg-vsc-surface border border-vsc-border rounded p-6 w-80"
      >
        <img src={import.meta.env.BASE_URL + "logo.png"} alt="FPS" className="h-16 w-16 mx-auto mb-3" />
        <h1 className="text-sm text-vsc-accent font-bold mb-4 text-center">
          FPS Dashboard
        </h1>
        {error && (
          <div className="text-xs text-vsc-error mb-3 text-center">
            {error}
          </div>
        )}
        <label className="block text-xs text-vsc-muted mb-1">Username</label>
        <input
          type="text"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          className="w-full bg-vsc-bg border border-vsc-border rounded px-2 py-1.5 text-sm text-vsc-text mb-3 outline-none focus:border-vsc-accent"
          autoFocus
        />
        <label className="block text-xs text-vsc-muted mb-1">Password</label>
        <input
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          className="w-full bg-vsc-bg border border-vsc-border rounded px-2 py-1.5 text-sm text-vsc-text mb-4 outline-none focus:border-vsc-accent"
        />
        <button
          type="submit"
          disabled={loading}
          className="w-full bg-vsc-accent text-vsc-bg text-sm font-bold rounded py-1.5 hover:opacity-90 disabled:opacity-50 transition-opacity"
        >
          {loading ? "..." : "Login"}
        </button>
      </form>
    </div>
  );
}
