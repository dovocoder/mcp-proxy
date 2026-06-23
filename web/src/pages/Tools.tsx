import { useQuery } from '@tanstack/react-query';
import { Wrench, Server } from 'lucide-react';
import { tools as toolsApi } from '../api/client';
import { Link } from 'react-router-dom';

export default function Tools() {
  const { data: toolList } = useQuery({ queryKey: ['tools'], queryFn: toolsApi.list });

  // Group tools by server
  const byServer = toolList?.reduce((acc, tool) => {
    if (!acc[tool.server_name]) acc[tool.server_name] = [];
    acc[tool.server_name].push(tool);
    return acc;
  }, {} as Record<string, typeof toolList>);

  return (
    <div className="space-y-6 pb-20 lg:pb-0">
      {/* Header */}
      <div>
        <h1 className="text-xl sm:text-2xl font-bold text-white">Tools</h1>
        <p className="text-sm text-slate-500 mt-1">
          All MCP tools aggregated from connected servers ({toolList?.length ?? 0} total)
        </p>
      </div>

      {toolList?.length === 0 && (
        <div className="bg-slate-900 rounded-xl border border-slate-800 p-8 sm:p-12 text-center">
          <Wrench className="w-12 h-12 text-slate-700 mx-auto mb-3" />
          <p className="text-sm text-slate-500">
            No tools discovered. Connect a server to see its tools.
          </p>
        </div>
      )}

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 sm:gap-6">
        {Object.entries(byServer || {}).map(([serverName, serverTools]) => (
          <div
            key={serverName}
            className="bg-slate-900 rounded-xl border border-slate-800 flex flex-col"
          >
            <div className="px-4 sm:px-5 py-4 border-b border-slate-800 flex items-center gap-2">
              <Server className="w-4 h-4 text-slate-400 flex-shrink-0" />
              <Link
                to="/servers"
                className="font-semibold text-white hover:text-brand-300 transition-colors truncate"
              >
                {serverName}
              </Link>
              <span className="text-sm text-slate-500 flex-shrink-0">
                · {serverTools?.length ?? 0}
              </span>
            </div>
            <div className="divide-y divide-slate-800 flex-1">
              {serverTools?.map((tool) => (
                <div key={tool.name} className="px-4 sm:px-5 py-3">
                  <div className="flex items-start gap-3">
                    <Wrench className="w-4 h-4 text-slate-500 mt-0.5 flex-shrink-0" />
                    <div className="flex-1 min-w-0">
                      <div className="font-medium text-white text-sm truncate">{tool.name}</div>
                      {tool.description && (
                        <div className="text-xs text-slate-500 mt-0.5 break-words">
                          {tool.description}
                        </div>
                      )}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
