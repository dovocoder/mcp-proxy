import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { Plus, Trash2, RefreshCw, Power, Server as ServerIcon } from 'lucide-react';
import { servers as serversApi, type Server } from '../api/client';

export default function Servers() {
  const queryClient = useQueryClient();
  const { data: srvList } = useQuery({ queryKey: ['servers'], queryFn: serversApi.list });
  const [showForm, setShowForm] = useState(false);

  const deleteMutation = useMutation({
    mutationFn: serversApi.delete,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['servers'] }),
  });

  const reconnectMutation = useMutation({
    mutationFn: serversApi.reconnect,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['servers'] }),
  });

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-white">Servers</h1>
          <p className="text-slate-500 mt-1">Manage backend MCP server connections</p>
        </div>
        <button
          onClick={() => setShowForm(true)}
          className="flex items-center gap-2 px-4 py-2 bg-brand-600 hover:bg-brand-700 text-white rounded-lg font-medium transition-colors"
        >
          <Plus className="w-4 h-4" />
          Add Server
        </button>
      </div>

      {showForm && <ServerForm onClose={() => setShowForm(false)} />}

      <div className="space-y-3">
        {srvList?.length === 0 && !showForm && (
          <div className="bg-slate-900 rounded-xl border border-slate-800 p-12 text-center">
            <ServerIcon className="w-12 h-12 text-slate-700 mx-auto mb-3" />
            <p className="text-slate-500">No servers configured yet</p>
            <button
              onClick={() => setShowForm(true)}
              className="mt-4 text-brand-400 hover:text-brand-300 font-medium"
            >
              Add your first server →
            </button>
          </div>
        )}

        {srvList?.map((srv) => (
          <div key={srv.id} className="bg-slate-900 rounded-xl border border-slate-800 p-5">
            <div className="flex items-center justify-between">
              <Link to={`/servers/${srv.id}`} className="flex items-center gap-3 flex-1">
                <div className={`w-3 h-3 rounded-full ${
                  srv.status === 'connected' ? 'bg-emerald-400' :
                  srv.status === 'error' ? 'bg-red-400' : 'bg-slate-600'
                }`} />
                <div>
                  <div className="font-semibold text-white hover:text-brand-300 transition-colors">
                    {srv.name}
                  </div>
                  <div className="text-sm text-slate-500">
                    {srv.transport === 'stdio' ? `${srv.command} ${(srv.args || []).join(' ')}` : srv.url}
                  </div>
                </div>
              </Link>

              <div className="flex items-center gap-3">
                <span className="text-sm text-slate-500">{srv.tools_count ?? 0} tools</span>
                <span className={`text-xs font-medium px-2.5 py-1 rounded-full ${
                  srv.status === 'connected'
                    ? 'bg-emerald-950/50 text-emerald-400'
                    : srv.status === 'error'
                    ? 'bg-red-950/50 text-red-400'
                    : 'bg-slate-800 text-slate-400'
                }`}>
                  {srv.status}
                </span>
                <button
                  onClick={() => reconnectMutation.mutate(srv.id)}
                  className="p-2 text-slate-400 hover:text-white hover:bg-slate-800 rounded-lg transition-colors"
                  title="Reconnect"
                >
                  <RefreshCw className="w-4 h-4" />
                </button>
                <button
                  onClick={() => deleteMutation.mutate(srv.id)}
                  className="p-2 text-slate-400 hover:text-red-400 hover:bg-slate-800 rounded-lg transition-colors"
                  title="Delete"
                >
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
            </div>

            {srv.live_error && (
              <div className="mt-3 text-xs text-red-400 bg-red-950/30 border border-red-900/50 rounded-lg px-3 py-2">
                {srv.live_error}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

function ServerForm({ onClose }: { onClose: () => void }) {
  const queryClient = useQueryClient();
  const [name, setName] = useState('');
  const [transport, setTransport] = useState('stdio');
  const [command, setCommand] = useState('');
  const [args, setArgs] = useState('');
  const [url, setUrl] = useState('');
  const [headers, setHeaders] = useState('');
  const [env, setEnv] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError('');

    try {
      const data: Partial<Server> = {
        name,
        transport,
        enabled: true,
        timeout: 120,
        connect_timeout: 60,
      };

      if (transport === 'stdio') {
        data.command = command;
        data.args = args.split(' ').filter(Boolean);
      } else {
        data.url = url;
        if (headers) {
          const hdrs: Record<string, string> = {};
          headers.split('\n').forEach((line) => {
            const [k, ...v] = line.split(':');
            if (k && v.length) hdrs[k.trim()] = v.join(':').trim();
          });
          data.headers = hdrs;
        }
      }

      if (env) {
        const envVars: Record<string, string> = {};
        env.split('\n').forEach((line) => {
          const [k, ...v] = line.split('=');
          if (k && v.length) envVars[k.trim()] = v.join('=').trim();
        });
        data.env = envVars;
      }

      await serversApi.create(data);
      queryClient.invalidateQueries({ queryKey: ['servers'] });
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create server');
    } finally {
      setLoading(false);
    }
  };

  return (
    <form onSubmit={handleSubmit} className="bg-slate-900 rounded-xl border border-slate-800 p-6 space-y-4">
      <h2 className="text-lg font-semibold text-white">Add MCP Server</h2>

      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="block text-sm font-medium text-slate-300 mb-1.5">Name</label>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-brand-500"
            placeholder="github"
            required
          />
        </div>
        <div>
          <label className="block text-sm font-medium text-slate-300 mb-1.5">Transport</label>
          <select
            value={transport}
            onChange={(e) => setTransport(e.target.value)}
            className="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-brand-500"
          >
            <option value="stdio">stdio</option>
            <option value="http">http</option>
          </select>
        </div>
      </div>

      {transport === 'stdio' ? (
        <>
          <div>
            <label className="block text-sm font-medium text-slate-300 mb-1.5">Command</label>
            <input
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              className="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-brand-500"
              placeholder="npx"
              required
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-slate-300 mb-1.5">Args (space-separated)</label>
            <input
              value={args}
              onChange={(e) => setArgs(e.target.value)}
              className="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-brand-500"
              placeholder="-y @modelcontextprotocol/server-github"
            />
          </div>
        </>
      ) : (
        <>
          <div>
            <label className="block text-sm font-medium text-slate-300 mb-1.5">URL</label>
            <input
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              className="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-brand-500"
              placeholder="https://mcp.example.com/v1"
              required
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-slate-300 mb-1.5">Headers (one per line, key: value)</label>
            <textarea
              value={headers}
              onChange={(e) => setHeaders(e.target.value)}
              rows={3}
              className="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-brand-500 font-mono text-sm"
              placeholder="Authorization: Bearer token"
            />
          </div>
        </>
      )}

      <div>
        <label className="block text-sm font-medium text-slate-300 mb-1.5">Environment Variables (one per line, KEY=value)</label>
        <textarea
          value={env}
          onChange={(e) => setEnv(e.target.value)}
          rows={3}
          className="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-brand-500 font-mono text-sm"
          placeholder="GITHUB_PERSONAL_ACCESS_TOKEN=ghp_..."
        />
      </div>

      {error && (
        <div className="text-sm text-red-400 bg-red-950/50 border border-red-900 rounded-lg px-3 py-2">
          {error}
        </div>
      )}

      <div className="flex gap-3">
        <button
          type="submit"
          disabled={loading}
          className="px-4 py-2 bg-brand-600 hover:bg-brand-700 disabled:opacity-50 text-white rounded-lg font-medium transition-colors"
        >
          {loading ? 'Creating...' : 'Create Server'}
        </button>
        <button
          type="button"
          onClick={onClose}
          className="px-4 py-2 bg-slate-800 hover:bg-slate-700 text-slate-300 rounded-lg font-medium transition-colors"
        >
          Cancel
        </button>
      </div>
    </form>
  );
}
