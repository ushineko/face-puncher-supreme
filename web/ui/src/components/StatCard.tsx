interface StatCardProps {
  title: string;
  children: React.ReactNode;
}

export default function StatCard({ title, children }: StatCardProps) {
  return (
    <div className="bg-vsc-surface border border-vsc-border rounded p-4">
      <h3 className="text-xs text-vsc-muted uppercase tracking-wider mb-3">
        {title}
      </h3>
      {children}
    </div>
  );
}

interface StatRowProps {
  label: string;
  value: string | number;
  accent?: boolean;
}

export function StatRow({ label, value, accent }: StatRowProps) {
  return (
    <div className="flex justify-between text-sm py-0.5">
      <span className="text-vsc-muted">{label}</span>
      <span className={accent ? "text-vsc-accent" : ""}>{value}</span>
    </div>
  );
}
