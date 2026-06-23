import { useQuery } from '@tanstack/react-query';
import { Server as ServerIcon, KeyRound, Wrench, CheckCircle2, XCircle, Activity, Layers, Brain } from 'lucide-react';
import { Link } from 'react-router-dom';
import { dashboard, servers as serversApi } from '../api/client';
import { Card, CardContent } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';

export default function Dashboard() {
  const { data: stats } = useQuery({ queryKey: ['dashboard'], queryFn: dashboard.stats });
  const { data: srvList } = useQuery({ queryKey: ['servers'], queryFn: serversApi.list });

  const cards = [
    { label: 'Servers', value: stats?.total_servers ?? 0, icon: ServerIcon },
    { label: 'Connected', value: stats?.connected_servers ?? 0, icon: CheckCircle2 },
    { label: 'Tools', value: stats?.total_tools ?? 0, icon: Wrench },
    { label: 'API Keys', value: stats?.total_api_keys ?? 0, icon: KeyRound },
    { label: 'Compounds', value: stats?.total_compounds ?? 0, icon: Layers },
    { label: 'Memories', value: stats?.total_memories ?? 0, icon: Brain },
  ];

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl sm:text-2xl font-bold text-foreground">Dashboard</h1>
        <p className="text-muted-foreground mt-1 text-sm">Overview of your MCP gateway</p>
      </div>

      {/* Stats cards */}
      <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-3 sm:gap-4">
        {cards.map((card) => (
          <Card key={card.label}>
            <CardContent className="flex flex-col gap-2">
              <div className="inline-flex items-center justify-center w-9 h-9 rounded-lg bg-muted">
                <card.icon className="w-4 h-4 text-foreground" />
              </div>
              <div className="text-2xl font-bold text-foreground">{card.value}</div>
              <div className="text-xs text-muted-foreground">{card.label}</div>
            </CardContent>
          </Card>
        ))}
      </div>

      {/* Server health */}
      <Card>
        <div className="px-4 sm:px-5 py-4 border-b border-border flex items-center gap-2">
          <Activity className="w-4 h-4 text-muted-foreground" />
          <h2 className="font-semibold text-foreground">Server Health</h2>
        </div>
        <div className="divide-y divide-border">
          {srvList?.length === 0 && (
            <div className="px-5 py-8 text-center text-muted-foreground">
              <p className="text-sm">No servers configured yet.</p>
              <Link
                to="/servers"
                className="mt-2 inline-block text-primary hover:opacity-80 font-medium text-sm"
              >
                Add your first server →
              </Link>
            </div>
          )}
          {srvList?.map((srv) => (
            <Link
              key={srv.id}
              to={`/servers/${srv.id}`}
              className="px-4 sm:px-5 py-3 flex items-center justify-between hover:bg-accent transition-colors"
            >
              <div className="flex items-center gap-3 min-w-0 flex-1">
                {srv.status === 'connected' ? (
                  <CheckCircle2 className="w-5 h-5 text-emerald-400 flex-shrink-0" />
                ) : (
                  <XCircle className="w-5 h-5 text-destructive flex-shrink-0" />
                )}
                <div className="min-w-0">
                  <div className="font-medium text-foreground truncate">{srv.name}</div>
                  <div className="text-xs text-muted-foreground truncate">
                    {srv.transport} · {srv.tools_count ?? 0} tools
                  </div>
                </div>
              </div>
              <Badge
                variant={
                  srv.status === 'connected'
                    ? 'secondary'
                    : srv.status === 'error'
                    ? 'destructive'
                    : 'outline'
                }
                className={cn(
                  'flex-shrink-0 ml-2',
                  srv.status === 'connected' && 'border-emerald-500/30 text-emerald-400 bg-emerald-500/10'
                )}
              >
                {srv.status}
              </Badge>
            </Link>
          ))}
        </div>
      </Card>
    </div>
  );
}
