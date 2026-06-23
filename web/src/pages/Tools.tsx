import { useQuery } from '@tanstack/react-query';
import { Wrench, Server } from 'lucide-react';
import { tools as toolsApi, Tool } from '../api/client';
import { Link } from 'react-router-dom';
import { Card, CardHeader, CardTitle, CardContent } from '@/components/ui/card';

export default function Tools() {
  const { data: toolList } = useQuery({ queryKey: ['tools'], queryFn: toolsApi.list });

  // Group tools by server
  const byServer = toolList?.reduce((acc, tool) => {
    if (!acc[tool.server_name]) acc[tool.server_name] = [];
    acc[tool.server_name].push(tool);
    return acc;
  }, {} as Record<string, Tool[]>);

  return (
    <div className="space-y-6 pb-20 lg:pb-0">
      {/* Header */}
      <div>
        <h1 className="text-xl sm:text-2xl font-bold text-foreground">Tools</h1>
        <p className="text-sm text-muted-foreground mt-1">
          All MCP tools aggregated from connected servers ({toolList?.length ?? 0} total)
        </p>
      </div>

      {toolList?.length === 0 && (
        <Card>
          <CardContent className="p-8 sm:p-12 text-center flex flex-col items-center gap-3">
            <Wrench className="w-12 h-12 text-muted-foreground/50" />
            <p className="text-sm text-muted-foreground">
              No tools discovered. Connect a server to see its tools.
            </p>
          </CardContent>
        </Card>
      )}

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 sm:gap-6">
        {Object.entries(byServer || {}).map(([serverName, serverTools]) => (
          <Card key={serverName} className="flex flex-col">
            <CardHeader className="border-b border-border">
              <div className="flex items-center gap-2 min-w-0">
                <Server className="w-4 h-4 text-muted-foreground flex-shrink-0" />
                <Link
                  to="/servers"
                  className="font-semibold text-foreground hover:opacity-80 transition-opacity truncate"
                >
                  <CardTitle className="truncate">{serverName}</CardTitle>
                </Link>
                <span className="text-sm text-muted-foreground flex-shrink-0">
                  · {serverTools?.length ?? 0}
                </span>
              </div>
            </CardHeader>
            <CardContent className="divide-y divide-border p-0 flex-1">
              {serverTools?.map((tool) => (
                <div key={tool.name} className="px-4 sm:px-5 py-3">
                  <div className="flex items-start gap-3">
                    <Wrench className="w-4 h-4 text-muted-foreground mt-0.5 flex-shrink-0" />
                    <div className="flex-1 min-w-0">
                      <div className="font-medium text-foreground text-sm truncate">{tool.name}</div>
                      {tool.description && (
                        <div className="text-sm text-muted-foreground mt-0.5 break-words">
                          {tool.description}
                        </div>
                      )}
                    </div>
                  </div>
                </div>
              ))}
            </CardContent>
          </Card>
        ))}
      </div>
    </div>
  );
}
