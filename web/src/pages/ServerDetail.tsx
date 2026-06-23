import { useParams, Link } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, RefreshCw, Trash2, Wrench } from 'lucide-react';
import { servers as serversApi, tools as toolsApi } from '../api/client';

export default function ServerDetail() {
  const { id } = useParams<{ id: string }>();
  const queryClient = useQueryClient();

  const { data } = useQuery({
    queryKey: ['server', id],
    queryFn: () => serversApi.get(id!),
    enabled: !!id,
  });

  const { data: allTools } = useQuery({
    queryKey: ['tools'],
    queryFn: toolsApi.list,
  });

  const reconnectMutation = useMutation({
    mutationFn: () => serversApi.reconnect(id!),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['server', id] }),
  });

  const deleteMutation = useMutation({
    mutationFn: () => serversApi.delete(id!),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['servers'] });
      window.history.back();
    },
  });

  if (!data) return <div className="text-slate-500">Loading...</div>;

  const srv = data.server;
  const serverTools = allTools?.filter((t) => t.server_id === id) || [];

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-4">
        <Link to="/servers" className="p-2 text-slate-400 hover:text-white hover:bg-slate-800 rounded-lg transition-colors">
          <ArrowLeft className="w-5 h-5" />
        </Link>
        <div className="flex-1">
          <h1 className="text-2xl font-bold text-white">{srv.name}</h1>
          <p className="text-slate-500 mt-1">{srv.transport} transport</p>
        </div>
        <span className={`text-xs font-medium px-3 py-1 rounded-full ${
          srv.status === 'connected'
            ? 'bg-emerald-950/50 text-emerald-400'
            : srv.status === 'error'
            ? 'bg-red-950/50 text-red-400'
            : 'bg-slate-800 text-slate-400'
        }`}>
          {srv.status}
        </span>
      </div>

      {data.live_error && (
        <div className="text-sm text-red-400 bg-red-950/30 border border-red-900/50 rounded-lg px-4 py-3">
          {data.live_error}
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* Configuration */}
        <div className="bg-slate-900 rounded-xl border border-slate-800 p-6">
          <h2 className="font-semibold text-white mb-4">Configuration</h2>
          <dl className="space-y-3">
            <div className="flex justify-between">
              <dt className="text-sm text-slate-500">Transport</dt>
              <dd className="text-sm text-slate-300 font-mono">{srv.transport}</dd>
            </div>
            {srv.transport === 'stdio' ? (
              <>
                <div className="flex justify-between">
                  <dt className="text-sm text-slate-500">Command</dt>
                  <dd className="text-sm text-slate-300 font-mono">{srv.command}</dd>
                </div>
                {srv.args && srv.args.length > 0 && (
                  <div>
                    <dt className="text-sm text-slate-500 mb-1">Args</dt>
                    <dd className="text-sm text-slate-300 font-mono bg-slate-800 rounded-lg px-3 py-2">
                      {srv.args.join(' ')}
                    </dd>
                  </div>
                )}
              </>
            ) : (
              <div className="flex justify-between">
                <dt className="text-sm text-slate-500">URL</dt>
                <dd className="text-sm text-slate-300 font-mono">{srv.url}</dd>
              </div>
            )}
            <div className="flex justify-between">
              <dt className="text-sm text-slate-500">Timeout</dt>
              <dd className="text-sm text-slate-300">{srv.timeout}s</dd>
            </div>
            <div className="flex justify-between">
              <dt className="text-sm text-slate-500">Connect Timeout</dt>
              <dd className="text-sm text-slate-300">{srv.connect_timeout}s</dd>
            </div>
            <div className="flex justify-between">
              <dt className="text-sm text-slate-500">Enabled</dt>
              <dd className="text-sm text-slate-300">{srv.enabled ? 'Yes' : 'No'}</dd>
            </div>
            {srv.env && Object.keys(srv.env).length > 0 && (
              <div>
                <dt className="text-sm text-slate-500 mb-1">Environment</dt>
                <dd className="text-sm text-slate-300 font-mono bg-slate-800 rounded-lg px-3 py-2">
                  {Object.keys(srv.env).map((k) => `${k}=***`).join('\n')}
                </dd>
              </div>
            )}
          </dl>

          <div className="flex gap-3 mt-6">
            <button
              onClick={() => reconnectMutation.mutate()}
              className="flex items-center gap-2 px-4 py-2 bg-brand-600 hover:bg-brand-700 text-white rounded-lg font-medium transition-colors"
            >
              <RefreshCw className="w-4 h-4" />
              Reconnect
            </button>
            <button
              onClick={() => deleteMutation.mutate()}
              className="flex items-center gap-2 px-4 py-2 bg-red-950/50 hover:bg-red-900/50 text-red-400 border border-red-900 rounded-lg font-medium transition-colors"
            >
              <Trash2 className="w-4 h-4" />
              Delete
            </button>
          </div>
        </div>

        {/* Tools */}
        <div className="bg-slate-900 rounded-xl border border-slate-800 p-6">
          <div className="flex items-center gap-2 mb-4">
            <Wrench className="w-4 h-4 text-slate-400" />
            <h2 className="font-semibold text-white">Tools ({serverTools.length})</h2>
          </div>
          <div className="space-y-2 max-h-96 overflow-y-auto">
            {serverTools.length === 0 ? (
              <p className="text-sm text-slate-500 text-center py-8">No tools discovered</p>
            ) : (
              serverTools.map((tool) => (
                <div key={tool.name} className="bg-slate-800 rounded-lg px-4 py-3">
                  <div className="font-medium text-white text-sm">{tool.name}</div>
                  {tool.description && (
                    <div className="text-xs text-slate-500 mt-1">{tool.description}</div>
                  )}
                </div>
              ))
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
