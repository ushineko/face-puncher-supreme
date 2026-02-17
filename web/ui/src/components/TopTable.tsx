interface TopTableProps {
  title: string;
  items: { label: string; value: number }[];
  emptyText?: string;
}

export default function TopTable({
  title,
  items,
  emptyText = "No data",
}: TopTableProps) {
  return (
    <div className="bg-vsc-surface border border-vsc-border rounded p-4">
      <h3 className="text-xs text-vsc-muted uppercase tracking-wider mb-3">
        {title}
      </h3>
      {items.length === 0 ? (
        <p className="text-xs text-vsc-muted">{emptyText}</p>
      ) : (
        <div className="max-h-64 overflow-auto">
          <table className="w-full text-xs">
            <tbody>
              {items.map((item, i) => (
                <tr
                  key={i}
                  className="border-b border-vsc-border last:border-0"
                >
                  <td className="py-1 pr-2 text-vsc-muted w-6 text-right">
                    {i + 1}
                  </td>
                  <td className="py-1 truncate max-w-0 w-full">{item.label}</td>
                  <td className="py-1 pl-2 text-right text-vsc-accent whitespace-nowrap">
                    {item.value.toLocaleString()}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
