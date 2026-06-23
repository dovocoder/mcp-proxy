import { useQuery } from '@tanstack/react-query';
import { Server as ServerIcon, KeyRound, Wrench, CheckCircle2, XCircle, Activity, Layers } from 'lucide-react';
import { Link } from 'react-router-dom';
import { dashboard, servers as serversApi } from '../api/client';

export default function Dashboard() {
  const { data: stats } = useQuery({ queryKey: ['dashboard'], queryFn: dashboard.stats });
  const { data: srvList } = useQuery({ queryKey: ['servers'], queryFn: serversApi.list });

  const cards = [
    { label: 'Servers', value: stats?.total_servers ?? 0, icon: ServerIcon, color: 'text-brand-400', bg: 'bg-brand-950/50' },
    { label: 'Connected', value: stats?.connected_servers ?? 0, icon: CheckCircle2, color: 'text-emerald-400', bg: 'bg-emerald-950/50' },
    { label: 'Tools', value: stats?.total_tools ?? 0, icon: Wrench, color: 'text-amber-400', bg: 'bg-amber-950/50' },
    { label: 'API Keys', value: stats?.total_api_keys ?? 0, icon: KeyRound, color: 'text-purple-400', bg: 'bg-purple-950/50' },
    { label: 'Compounds', value: stats?.total_compounds ?? 0, icon: Layers, color: 'text-cyan-400', bg: 'bg-cyan-950/50' },
  ];

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl sm:text-2xl font-bold text-white">Dashboard</h1>
        <p className="text-slate-500 mt-1 text-sm">Overview of your MCP gateway</p>
      </div>

      {/* Stats cards */}
      <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3 sm:gap-4">
        {cards.map((card) => (
          <div key={card.label} className="bg-slate-900 rounded-xl border border-slate-800 p-4">
            <div className={`inline-flex items-center justify-center w-9 h-9 rounded-lg ${card.bg} mb-2`}>
              <card.icon className={`w-4 h-4 ${card.color}`} />
            </div>
            <div className="text-2xl font-bold text-white">{card.value}</div>
            <div className="text-xs text-slate-500 mt-0.5">{card.label}</div>
          </div>
        ))}
      </div>

      {/* Server health */}
      <div className="bg-slate-900 rounded-xl border border-slate-800">
        <div className="px-4 sm:px-5 py-4 border-b border-slate-800 flex items-center gap-2">
          <Activity className="w-4 h-4 text-slate-400" />
          <h2 className="font-semibold text-white">Server Health</h2>
        </div>
        <div className="divide-y divide-slate-800">
          {srvList?.length === 0 && (
            <div className="px-5 py-8 text-center text-slate-500">
              <p className="text-sm">No servers configured yet.</p>
              <Link to="/servers" className="mt-2 inline-block text-brand-400 hover:text-brand-300 font-medium text-sm">
                Add your first server →
              </Link>
            </div>
          )}
          {srvList?.map((srv) => (
            <Link
              key={srv.id}
              to={`/servers/${srv.id}`}
              className="px-4 sm:px-5 py-3 flex items-center justify-between hover:bg-slate-800/50 transition-colors"
            >
              <div className="flex items-center gap-3 min-w-0 flex-1">
                {srv.status === 'connected' ? (
                  <CheckCircle2 className="w-5 h-5 text-emerald-400 flex-shrink-0" />
                ) : (
                  <XCircle className="w-5 h-5 text-red-400 flex-shrink-0" />
                )}
                <div className="min-w-0">
                  <div className="font-medium text-white truncate">{srv.name}</div>
                  <div className="text-xs text-slate-500 truncate">
                    {srv.transport} · {srv.tools_count ?? 0} tools
                  </div>
                </div>
              </div>
              <div className={`text-xs font-medium px-2.5 py-1 rounded-full flex-shrink-0 ml-2 ${
                srv.status === 'connected'
                  ? 'bg-emerald-950/50 text-emerald-400'
                  : srv.status === 'error'
                  ? 'bg-red-950/50 text-red-400'
                  : 'bg-slate-800 text-slate-400'
              }`}>
                {srv.status}
              </div>
            </Link>
          ))}
        </div>
      </div>
    </div>
  );
}
