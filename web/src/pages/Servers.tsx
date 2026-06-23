import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { Plus, Trash2, RefreshCw, Server as ServerIcon, Cloud, Zap, Terminal } from 'lucide-react';
import { servers as serversApi, type Server } from '../api/client';

const PRESETS = [
  {
    id: 'azure-devops',
    name: 'Azure DevOps',
    icon: Cloud,
    description: 'Microsoft hosted MCP server',
    config: {
      name: 'azure-devops',
      transport: 'streamable-http',
      url: 'https://mcp.dev.azure.com/',
      urlHint: 'https://mcp.dev.azure.com/{your-organization}',
    },
  },
  {
    id: 'azure-devops-local',
    name: 'Azure DevOps (Local)',
    icon: Terminal,
    description: 'Local stdio server via npx',
    config: {
      name: 'azure-devops-local',
      transport: 'stdio',
      command: 'npx',
      args: '-y @azure-devops/mcp',
    },
  },
  {
    id: 'github',
    name: 'GitHub MCP',
    icon: Zap,
    description: 'GitHub MCP server via npx',
    config: {
      name: 'github',
      transport: 'stdio',
      command: 'npx',
      args: '-y @modelcontextprotocol/server-github',
    },
  },
  {
    id: 'custom',
    name: 'Custom',
    icon: Plus,
    description: 'Configure manually',
    config: null,
  },
];

export default function Servers() {
  const queryClient = useQueryClient();
  const { data: srvList } = useQuery({ queryKey: ['servers'], queryFn: serversApi.list });
  const [showForm, setShowForm] = useState(false);
  const [preset, setPreset] = useState<string | null>(null);

  const deleteMutation = useMutation({
    mutationFn: serversApi.delete,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['servers'] }),
  });

  const reconnectMutation = useMutation({
    mutationFn: serversApi.reconnect,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['servers'] }),
  });

  const handleAddClick = () => {
    setShowForm(true);
    setPreset(null);
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl sm:text-2xl font-bold text-white">Servers</h1>
          <p className="text-slate-500 mt-1 text-sm">Manage backend MCP server connections</p>
        </div>
        <button
          onClick={handleAddClick}
          className="flex items-center gap-2 px-3 sm:px-4 py-2 bg-brand-600 hover:bg-brand-700 text-white rounded-lg font-medium text-sm transition-colors"
        >
          <Plus className="w-4 h-4" />
          <span className="hidden sm:inline">Add Server</span>
          <span className="sm:hidden">Add</span>
        </button>
      </div>

      {showForm && !preset && (
        <div className="bg-slate-900 rounded-xl border border-slate-800 p-4 sm:p-6">
          <h2 className="text-lg font-semibold text-white mb-4">Choose a preset</h2>
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
            {PRESETS.map((p) => (
              <button
                key={p.id}
                onClick={() => setPreset(p.id)}
                className="flex flex-col items-center gap-2 p-4 bg-slate-800 hover:bg-slate-700 border border-slate-700 hover:border-brand-500 rounded-lg transition-colors text-center"
              >
                <p.icon className="w-6 h-6 text-brand-400" />
                <div className="font-medium text-white text-sm">{p.name}</div>
                <div className="text-xs text-slate-500">{p.description}</div>
              </button>
            ))}
          </div>
          <button
            onClick={() => setShowForm(false)}
            className="mt-4 text-sm text-slate-400 hover:text-white"
          >
            Cancel
          </button>
        </div>
      )}

      {showForm && preset && (
        <ServerForm
          preset={PRESETS.find((p) => p.id === preset)!}
          onClose={() => { setShowForm(false); setPreset(null); }}
        />
      )}

      {!showForm && (
        <div className="space-y-3">
          {srvList?.length === 0 && (
            <div className="bg-slate-900 rounded-xl border border-slate-800 p-8 sm:p-12 text-center">
              <ServerIcon className="w-10 h-10 text-slate-700 mx-auto mb-3" />
              <p className="text-slate-500">No servers configured yet</p>
              <button
                onClick={handleAddClick}
                className="mt-3 text-brand-400 hover:text-brand-300 font-medium text-sm"
              >
                Add your first server →
              </button>
            </div>
          )}

          {srvList?.map((srv) => (
            <div key={srv.id} className="bg-slate-900 rounded-xl border border-slate-800 p-4">
              <div className="flex items-start justify-between gap-3">
                <Link to={`/servers/${srv.id}`} className="flex items-center gap-3 min-w-0 flex-1">
                  <div className={`w-2.5 h-2.5 rounded-full flex-shrink-0 mt-1 ${
                    srv.status === 'connected' ? 'bg-emerald-400' :
                    srv.status === 'error' ? 'bg-red-400' : 'bg-slate-600'
                  }`} />
                  <div className="min-w-0">
                    <div className="font-semibold text-white hover:text-brand-300 transition-colors truncate">
                      {srv.name}
                    </div>
                    <div className="text-sm text-slate-500 truncate">
                      {srv.transport === 'stdio'
                        ? `${srv.command} ${(srv.args || []).join(' ')}`
                        : srv.url}
                    </div>
                  </div>
                </Link>

                <div className="flex items-center gap-2 flex-shrink-0">
                  <span className={`text-xs font-medium px-2 py-1 rounded-full ${
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

              <div className="mt-2 flex items-center gap-3 text-xs text-slate-600">
                <span className="uppercase tracking-wide">{srv.transport}</span>
                <span>{srv.tools_count ?? 0} tools</span>
              </div>

              {srv.live_error && (
                <div className="mt-2 text-xs text-red-400 bg-red-950/30 border border-red-900/50 rounded-lg px-3 py-2">
                  {srv.live_error}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

interface PresetConfig {
  name: string;
  transport: string;
  command?: string;
  args?: string;
  url?: string;
  urlHint?: string;
}

function ServerForm({ preset, onClose }: { preset: { id: string; name: string; config: PresetConfig | null }; onClose: () => void }) {
  const queryClient = useQueryClient();
  const cfg = preset.config;
  const [name, setName] = useState(cfg?.name || '');
  const [transport, setTransport] = useState(cfg?.transport || 'stdio');
  const [command, setCommand] = useState(cfg?.command || '');
  const [args, setArgs] = useState(cfg?.args || '');
  const [url, setUrl] = useState(cfg?.url || '');
  const [headers, setHeaders] = useState('');
  const [env, setEnv] = useState('');
  const [authToken, setAuthToken] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  const isHTTP = transport === 'http' || transport === 'streamable-http';

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
            const idx = line.indexOf(':');
            if (idx > 0) hdrs[line.slice(0, idx).trim()] = line.slice(idx + 1).trim();
          });
          data.headers = hdrs;
        }
      }

      if (authToken) {
        data.auth_token = authToken;
      }

      if (env) {
        const envVars: Record<string, string> = {};
        env.split('\n').forEach((line) => {
          const idx = line.indexOf('=');
          if (idx > 0) envVars[line.slice(0, idx).trim()] = line.slice(idx + 1).trim();
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
    <form onSubmit={handleSubmit} className="bg-slate-900 rounded-xl border border-slate-800 p-4 sm:p-6 space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold text-white">Add MCP Server</h2>
        <span className="text-xs text-slate-500 bg-slate-800 px-2 py-1 rounded">{preset.name}</span>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <div>
          <label className="block text-sm font-medium text-slate-300 mb-1.5">Name</label>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-brand-500 text-sm"
            placeholder="my-server"
            required
          />
        </div>
        <div>
          <label className="block text-sm font-medium text-slate-300 mb-1.5">Transport</label>
          <select
            value={transport}
            onChange={(e) => setTransport(e.target.value)}
            className="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-brand-500 text-sm"
          >
            <option value="stdio">stdio (local process)</option>
            <option value="streamable-http">streamable-http (remote)</option>
            <option value="http">http (legacy SSE)</option>
          </select>
        </div>
      </div>

      {transport === 'stdio' ? (
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div>
            <label className="block text-sm font-medium text-slate-300 mb-1.5">Command</label>
            <input
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              className="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-brand-500 font-mono text-sm"
              placeholder="npx"
              required
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-slate-300 mb-1.5">Args (space-separated)</label>
            <input
              value={args}
              onChange={(e) => setArgs(e.target.value)}
              className="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-brand-500 font-mono text-sm"
              placeholder="-y @modelcontextprotocol/server-github"
            />
          </div>
        </div>
      ) : (
        <div>
          <label className="block text-sm font-medium text-slate-300 mb-1.5">
            URL {cfg?.urlHint && <span className="text-slate-600 font-normal">({cfg.urlHint})</span>}
          </label>
          <input
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            className="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-brand-500 font-mono text-sm"
            placeholder="https://mcp.dev.azure.com/my-org"
            required
          />
        </div>
      )}

      {isHTTP && (
        <div>
          <label className="block text-sm font-medium text-slate-300 mb-1.5">
            Auth Token <span className="text-slate-600 font-normal">— optional, used as client_id for OAuth</span>
          </label>
          <input
            type="password"
            value={authToken}
            onChange={(e) => setAuthToken(e.target.value)}
            className="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-brand-500 font-mono text-sm"
            placeholder="Leave empty for Entra ID (auto-detected)"
          />
          <p className="text-xs text-slate-600 mt-1">
            For Entra ID / Azure DevOps: leave empty — OAuth will use a built-in public client. Enter a client_id only if you have a custom app registration.
          </p>
        </div>
      )}

      {isHTTP && (
        <div>
          <label className="block text-sm font-medium text-slate-300 mb-1.5">Additional Headers (one per line, key: value)</label>
          <textarea
            value={headers}
            onChange={(e) => setHeaders(e.target.value)}
            rows={3}
            className="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-brand-500 font-mono text-sm"
            placeholder="X-Custom-Header: value"
          />
        </div>
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
          className="px-4 py-2 bg-brand-600 hover:bg-brand-700 disabled:opacity-50 text-white rounded-lg font-medium text-sm transition-colors"
        >
          {loading ? 'Creating...' : 'Create Server'}
        </button>
        <button
          type="button"
          onClick={onClose}
          className="px-4 py-2 bg-slate-800 hover:bg-slate-700 text-slate-300 rounded-lg font-medium text-sm transition-colors"
        >
          Cancel
        </button>
      </div>
    </form>
  );
}
