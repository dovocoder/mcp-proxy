import { useParams, Link } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, RefreshCw, Trash2, Wrench, LogIn, ShieldCheck, ShieldAlert, ExternalLink, Link as LinkIcon } from 'lucide-react';
import { servers as serversApi, tools as toolsApi } from '../api/client';
import { useState } from 'react';

export default function ServerDetail() {
  const { id } = useParams<{ id: string }>();
  const queryClient = useQueryClient();
  const [authUrl, setAuthUrl] = useState<string | null>(null);

  const { data } = useQuery({
    queryKey: ['server', id],
    queryFn: () => serversApi.get(id!),
    enabled: !!id,
  });

  const { data: allTools } = useQuery({
    queryKey: ['tools'],
    queryFn: toolsApi.list,
  });

  const isHTTP = data?.server.transport === 'http' || data?.server.transport === 'streamable-http';

  const { data: authStatus } = useQuery({
    queryKey: ['auth-status', id],
    queryFn: () => serversApi.authStatus(id!),
    enabled: !!id && isHTTP,
    refetchInterval: 5000,
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

  const authMutation = useMutation({
    mutationFn: () => serversApi.initiateAuth(id!),
    onSuccess: (res) => {
      setAuthUrl(res.auth_url);
      window.open(res.auth_url, '_blank');
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

      {/* OAuth Authentication (for HTTP transports) */}
      {isHTTP && (
        <div className="bg-slate-900 rounded-xl border border-slate-800 p-5">
          <div className="flex items-center justify-between mb-3">
            <div className="flex items-center gap-2">
              {authStatus?.status === 'valid' ? (
                <ShieldCheck className="w-5 h-5 text-emerald-400" />
              ) : authStatus?.status === 'expired' ? (
                <ShieldAlert className="w-5 h-5 text-amber-400" />
              ) : (
                <ShieldAlert className="w-5 h-5 text-slate-500" />
              )}
              <h3 className="font-semibold text-white">OAuth Authentication</h3>
            </div>
            <button
              onClick={() => authMutation.mutate()}
              disabled={authMutation.isPending}
              className="flex items-center gap-2 px-3 py-1.5 bg-brand-600 hover:bg-brand-700 disabled:opacity-50 text-white rounded-lg text-sm font-medium transition-colors"
            >
              <LogIn className="w-4 h-4" />
              {authStatus?.status === 'valid' ? 'Re-authenticate' : 'Authenticate with Entra ID'}
            </button>
          </div>
          <div className="flex items-center gap-2 text-sm">
            <span className="text-slate-500">Status:</span>
            <span className={`font-medium ${
              authStatus?.status === 'valid' ? 'text-emerald-400' :
              authStatus?.status === 'expired' ? 'text-amber-400' : 'text-slate-400'
            }`}>
              {authStatus?.status === 'valid' ? 'Authenticated' :
               authStatus?.status === 'expired' ? 'Token expired — re-authenticate' :
               'Not authenticated'}
            </span>
          </div>
          {authMutation.error && (
            <div className="mt-2 text-xs text-red-400">
              {authMutation.error instanceof Error ? authMutation.error.message : 'Failed to initiate auth'}
            </div>
          )}
          {authUrl && (
            <div className="mt-2 text-xs text-slate-500">
              If the browser didn't open,{' '}
              <a href={authUrl} target="_blank" rel="noopener noreferrer" className="text-brand-400 hover:text-brand-300 inline-flex items-center gap-1">
                click here to authenticate <ExternalLink className="w-3 h-3" />
              </a>
            </div>
          )}
        </div>
      )}

      {/* Connection URLs */}
      <div className="bg-slate-900 rounded-xl border border-slate-800 p-5">
        <div className="flex items-center gap-2 mb-3">
          <LinkIcon className="w-4 h-4 text-brand-400" />
          <h3 className="font-semibold text-white">Connection URLs</h3>
        </div>
        <p className="text-xs text-slate-500 mb-3">Use these endpoints with an API key to connect MCP clients to this specific server.</p>
        <div className="space-y-2">
          <div className="flex items-center gap-2">
            <span className="text-xs font-mono px-1.5 py-0.5 bg-brand-950/50 text-brand-400 rounded">POST</span>
            <code className="text-xs text-slate-300 font-mono break-all">/api/servers/{id}/mcp</code>
          </div>
          <div className="flex items-center gap-2">
            <span className="text-xs font-mono px-1.5 py-0.5 bg-emerald-950/50 text-emerald-400 rounded">SSE</span>
            <code className="text-xs text-slate-300 font-mono break-all">/api/servers/{id}/sse</code>
          </div>
        </div>
      </div>

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
            {srv.auth_token && (
              <div className="flex justify-between">
                <dt className="text-sm text-slate-500">Auth Token</dt>
                <dd className="text-sm text-slate-300 font-mono">••••••••••••</dd>
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
