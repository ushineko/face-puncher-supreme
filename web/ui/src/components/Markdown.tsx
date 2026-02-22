import { Children, isValidElement } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

interface MarkdownProps {
  content: string;
}

function slugify(text: string): string {
  return text
    .toLowerCase()
    .replace(/[^\w\s-]/g, "")
    .replace(/\s+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "");
}

function extractText(children: React.ReactNode): string {
  let text = "";
  Children.forEach(children, (child) => {
    if (typeof child === "string") {
      text += child;
    } else if (isValidElement(child) && child.props?.children) {
      text += extractText(child.props.children);
    }
  });
  return text;
}

export default function Markdown({ content }: MarkdownProps) {
  return (
    <div className="prose-vsc">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          h1: ({ children }) => {
            const id = slugify(extractText(children));
            return (
              <h1 id={id} className="text-xl text-vsc-text font-bold mt-6 mb-3 border-b border-vsc-border pb-2">
                {children}
              </h1>
            );
          },
          h2: ({ children }) => {
            const id = slugify(extractText(children));
            return (
              <h2 id={id} className="text-lg text-vsc-text font-bold mt-5 mb-2 border-b border-vsc-border pb-1">
                {children}
              </h2>
            );
          },
          h3: ({ children }) => {
            const id = slugify(extractText(children));
            return (
              <h3 id={id} className="text-base text-vsc-text font-bold mt-4 mb-2">
                {children}
              </h3>
            );
          },
          p: ({ children }) => (
            <p className="text-sm text-vsc-text leading-relaxed mb-3">
              {children}
            </p>
          ),
          a: ({ href, children }) => {
            if (href?.startsWith("#")) {
              return (
                <a
                  href={href}
                  className="text-vsc-accent hover:underline"
                  onClick={(e) => {
                    e.preventDefault();
                    const target = document.getElementById(href.slice(1));
                    target?.scrollIntoView({ behavior: "smooth" });
                  }}
                >
                  {children}
                </a>
              );
            }
            return (
              <a
                href={href}
                className="text-vsc-accent hover:underline"
                target="_blank"
                rel="noopener noreferrer"
              >
                {children}
              </a>
            );
          },
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
          img: ({ src, alt }) => {
            const resolved = src?.startsWith("assets/")
              ? import.meta.env.BASE_URL + src.replace("assets/", "")
              : src;
            return <img src={resolved} alt={alt ?? ""} className="my-3 max-w-xs" />;
          },
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}
