import { useEffect, useState } from "react";
import { fetchReadme } from "../api";
import Markdown from "../components/Markdown";

export default function About() {
  const [content, setContent] = useState<string | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    fetchReadme()
      .then(setContent)
      .catch((e: Error) => setError(e.message));
  }, []);

  if (error) {
    return <p className="text-vsc-error text-sm">Failed to load README: {error}</p>;
  }

  if (content === null) {
    return <p className="text-vsc-muted text-sm">Loading...</p>;
  }

  return (
    <div className="max-w-4xl">
      <Markdown content={content} />
    </div>
  );
}
