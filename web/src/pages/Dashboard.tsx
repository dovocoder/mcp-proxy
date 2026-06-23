import { useQuery } from '@tanstack/react-query';
import { Server, KeyRound, Wrench, CheckCircle2, XCircle, Activity } from 'lucide-react';
import { dashboard, servers as serversApi } from '../api/client';

export default function Dashboard() {
  const { data: stats } = useQuery({ queryKey: ['dashboard'], queryFn: dashboard.stats });
  const { data: srvList } = useQuery({ queryKey: ['servers'], queryFn: serversApi.list });

  const cards = [
    {
      label: 'Total Servers',
      value: stats?.total_servers ?? 0,
      icon: Server,
      color: 'text-brand-400',
      bg: 'bg-brand-950/50',
    },
    {
      label: 'Connected',
      value: stats?.connected_servers ?? 0,
      icon: CheckCircle2,
      color: 'text-emerald-400',
      bg: 'bg-emerald-950/50',
    },
    {
      label: 'Total Tools',
      value: stats?.total_tools ?? 0,
      icon: Wrench,
      color: 'text-amber-400',
      bg: 'bg-amber-950/50',
    },
    {
      label: 'API Keys',
      value: stats?.total_api_keys ?? 0,
      icon: KeyRound,
      color: 'text-purple-400',
      bg: 'bg-purple-950/50',
    },
  ];

  return (
    <div className="space-y-8">
      <div>
        <h1 className="text-2xl font-bold text-white">Dashboard</h1>
        <p className="text-slate-500 mt-1">Overview of your MCP gateway</p>
      </div>

      {/* Stats cards */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        {cards.map((card) => (
          <div key={card.label} className="bg-slate-900 rounded-xl border border-slate-800 p-5">
            <div className={`inline-flex items-center justify-center w-10 h-10 rounded-lg ${card.bg} mb-3`}>
              <card.icon className={`w-5 h-5 ${card.color}`} />
            </div>
            <div className="text-3xl font-bold text-white">{card.value}</div>
            <div className="text-sm text-slate-500 mt-1">{card.label}</div>
          </div>
        ))}
      </div>

      {/* Server health */}
      <div className="bg-slate-900 rounded-xl border border-slate-800">
        <div className="px-5 py-4 border-b border-slate-800 flex items-center gap-2">
          <Activity className="w-4 h-4 text-slate-400" />
          <h2 className="font-semibold text-white">Server Health</h2>
        </div>
        <div className="divide-y divide-slate-800">
          {srvList?.length === 0 && (
            <div className="px-5 py-8 text-center text-slate-500">
              No servers configured. Add one from the Servers page.
            </div>
          )}
          {srvList?.map((srv) => (
            <div key={srv.id} className="px-5 py-3 flex items-center justify-between">
              <div className="flex items-center gap-3">
                {srv.status === 'connected' ? (
                  <CheckCircle2 className="w-5 h-5 text-emerald-400" />
                ) : (
                  <XCircle className="w-5 h-5 text-red-400" />
                )}
                <div>
                  <div className="font-medium text-white">{srv.name}</div>
                  <div className="text-xs text-slate-500">
                    {srv.transport} · {srv.tools_count ?? 0} tools
                  </div>
                </div>
              </div>
              <div className={`text-xs font-medium px-2.5 py-1 rounded-full ${
                srv.status === 'connected'
                  ? 'bg-emerald-950/50 text-emerald-400'
                  : srv.status === 'error'
                  ? 'bg-red-950/50 text-red-400'
                  : 'bg-slate-800 text-slate-400'
              }`}>
                {srv.status}
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
