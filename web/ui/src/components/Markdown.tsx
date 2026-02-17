import ReactMarkdown from "react-markdown";

interface MarkdownProps {
  content: string;
}

export default function Markdown({ content }: MarkdownProps) {
  return (
    <div className="prose-vsc">
      <ReactMarkdown
        components={{
          h1: ({ children }) => (
            <h1 className="text-xl text-vsc-text font-bold mt-6 mb-3 border-b border-vsc-border pb-2">
              {children}
            </h1>
          ),
          h2: ({ children }) => (
            <h2 className="text-lg text-vsc-text font-bold mt-5 mb-2 border-b border-vsc-border pb-1">
              {children}
            </h2>
          ),
          h3: ({ children }) => (
            <h3 className="text-base text-vsc-text font-bold mt-4 mb-2">
              {children}
            </h3>
          ),
          p: ({ children }) => (
            <p className="text-sm text-vsc-text leading-relaxed mb-3">
              {children}
            </p>
          ),
          a: ({ href, children }) => (
            <a
              href={href}
              className="text-vsc-accent hover:underline"
              target="_blank"
              rel="noopener noreferrer"
            >
              {children}
            </a>
          ),
          code: ({ className, children }) => {
            const isBlock = className?.includes("language-");
            if (isBlock) {
              return (
                <code className="block bg-vsc-bg border border-vsc-border rounded p-3 text-xs overflow-auto my-3">
                  {children}
                </code>
              );
            }
            return (
              <code className="bg-vsc-bg border border-vsc-border rounded px-1 py-0.5 text-xs text-vsc-warning">
                {children}
              </code>
            );
          },
          pre: ({ children }) => <pre className="my-0">{children}</pre>,
          ul: ({ children }) => (
            <ul className="list-disc list-inside text-sm text-vsc-text mb-3 space-y-1">
              {children}
            </ul>
          ),
          ol: ({ children }) => (
            <ol className="list-decimal list-inside text-sm text-vsc-text mb-3 space-y-1">
              {children}
            </ol>
          ),
          li: ({ children }) => <li className="text-sm">{children}</li>,
          table: ({ children }) => (
            <div className="overflow-auto my-3">
              <table className="text-xs border border-vsc-border w-full">
                {children}
              </table>
            </div>
          ),
          th: ({ children }) => (
            <th className="bg-vsc-header border border-vsc-border px-3 py-1.5 text-left text-vsc-accent">
              {children}
            </th>
          ),
          td: ({ children }) => (
            <td className="border border-vsc-border px-3 py-1.5">{children}</td>
          ),
          blockquote: ({ children }) => (
            <blockquote className="border-l-2 border-vsc-accent pl-3 my-3 text-vsc-muted">
              {children}
            </blockquote>
          ),
          hr: () => <hr className="border-vsc-border my-4" />,
          strong: ({ children }) => (
            <strong className="text-vsc-text font-bold">{children}</strong>
          ),
          em: ({ children }) => (
            <em className="text-vsc-warning italic">{children}</em>
          ),
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}
