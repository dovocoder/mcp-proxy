import { useState } from 'react';
import { useParams, Link, useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  Plus,
  Trash2,
  Layers,
  ArrowLeft,
  Server,
  CheckCircle2,
  XCircle,
  X,
  Link as LinkIcon,
  Copy,
  Check,
} from 'lucide-react';
import { compounds as compoundsApi, servers as serversApi } from '../api/client';

export default function CompoundServers() {
  const { id: selectedId } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [showCreate, setShowCreate] = useState(false);
  const [copiedUrl, setCopiedUrl] = useState<string | null>(null);

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
    setCopiedUrl(text);
    setTimeout(() => setCopiedUrl(null), 2000);
  };

  const { data: compounds } = useQuery({
    queryKey: ['compounds'],
    queryFn: compoundsApi.list,
  });

  const { data: detail } = useQuery({
    queryKey: ['compound', selectedId],
    queryFn: () => compoundsApi.get(selectedId!),
    enabled: !!selectedId,
  });

  const { data: allServers } = useQuery({
    queryKey: ['servers'],
    queryFn: serversApi.list,
  });

  const deleteMutation = useMutation({
    mutationFn: compoundsApi.delete,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['compounds'] });
      queryClient.invalidateQueries({ queryKey: ['dashboard'] });
    },
  });

  const addMemberMutation = useMutation({
    mutationFn: ({ compoundId, serverId }: { compoundId: string; serverId: string }) =>
      compoundsApi.addMember(compoundId, serverId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['compound', selectedId] });
    },
  });

  const removeMemberMutation = useMutation({
    mutationFn: ({ compoundId, serverId }: { compoundId: string; serverId: string }) =>
      compoundsApi.removeMember(compoundId, serverId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['compound', selectedId] });
    },
  });

  const renderCopyButton = (url: string) => (
    <button
      onClick={() => copyToClipboard(url)}
      className="flex-shrink-0 p-2 min-h-[40px] min-w-[40px] flex items-center justify-center bg-slate-800 hover:bg-slate-700 text-slate-300 rounded-lg transition-colors"
      aria-label="Copy to clipboard"
    >
      {copiedUrl === url ? (
        <Check className="w-4 h-4 text-emerald-400" />
      ) : (
        <Copy className="w-4 h-4" />
      )}
    </button>
  );

  // Detail view
  if (selectedId && detail) {
    const memberIds = new Set(detail.members.map((m) => m.id));
    const availableServers = allServers?.filter((s) => !memberIds.has(s.id)) || [];

    const mcpUrl = `/api/compounds/${selectedId}/mcp`;
    const sseUrl = `/api/compounds/${selectedId}/sse`;

    return (
      <div className="space-y-6 pb-20 lg:pb-0">
        {/* Header */}
        <div className="flex items-start gap-3 sm:gap-4">
          <Link
            to="/compounds"
            className="flex-shrink-0 p-2 text-slate-400 hover:text-white hover:bg-slate-800 rounded-lg transition-colors min-h-[40px] min-w-[40px] flex items-center justify-center"
          >
            <ArrowLeft className="w-5 h-5" />
          </Link>
          <div className="flex-1 min-w-0">
            <h1 className="text-xl sm:text-2xl font-bold text-white truncate">{detail.name}</h1>
            {detail.description && (
              <p className="text-sm text-slate-500 mt-1 break-words">{detail.description}</p>
            )}
          </div>
          <div className="flex items-center gap-4 sm:gap-6 flex-shrink-0">
            <div className="text-right">
              <div className="text-xl sm:text-2xl font-bold text-brand-400">{detail.tool_count}</div>
              <div className="text-xs text-slate-500">tools</div>
            </div>
            <div className="text-right">
              <div className="text-xl sm:text-2xl font-bold text-white">{detail.members.length}</div>
              <div className="text-xs text-slate-500">members</div>
            </div>
          </div>
        </div>

        {/* Members */}
        <div className="bg-slate-900 rounded-xl border border-slate-800">
          <div className="px-4 sm:px-5 py-4 border-b border-slate-800 flex flex-col sm:flex-row sm:items-center justify-between gap-3">
            <h2 className="font-semibold text-white">Member Servers</h2>
            {availableServers.length > 0 && (
              <div className="relative">
                <select
                  className="appearance-none w-full sm:w-auto bg-slate-800 border border-slate-700 text-slate-300 rounded-lg px-3 py-2 min-h-[40px] text-sm pr-8 focus:outline-none focus:border-brand-500 transition-colors"
                  onChange={(e) => {
                    if (e.target.value) {
                      addMemberMutation.mutate({ compoundId: selectedId, serverId: e.target.value });
                      e.target.value = '';
                    }
                  }}
                  defaultValue=""
                >
                  <option value="" disabled>
                    + Add server
                  </option>
                  {availableServers.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name} ({s.transport})
                    </option>
                  ))}
                </select>
              </div>
            )}
          </div>
          <div className="divide-y divide-slate-800">
            {detail.members.length === 0 && (
              <div className="px-5 py-8 text-center text-sm text-slate-500">
                No members yet. Add servers to this compound.
              </div>
            )}
            {detail.members.map((m) => (
              <div key={m.id} className="px-4 sm:px-5 py-3 flex items-center justify-between gap-3">
                <div className="flex items-center gap-3 min-w-0">
                  <Server className="w-4 h-4 text-slate-500 flex-shrink-0" />
                  <div className="min-w-0">
                    <div className="font-medium text-white truncate">{m.name}</div>
                    <div className="text-xs text-slate-500">
                      {m.transport} · {m.status}
                    </div>
                  </div>
                </div>
                <button
                  onClick={() =>
                    removeMemberMutation.mutate({ compoundId: selectedId, serverId: m.id })
                  }
                  className="flex-shrink-0 p-2 min-h-[40px] min-w-[40px] flex items-center justify-center text-slate-400 hover:text-red-400 hover:bg-slate-800 rounded-lg transition-colors"
                  aria-label="Remove member"
                >
                  <X className="w-4 h-4" />
                </button>
              </div>
            ))}
          </div>
        </div>

        {/* Connection URLs */}
        <div className="bg-slate-900 rounded-xl border border-slate-800 p-4 sm:p-5">
          <div className="flex items-center gap-2 mb-3">
            <LinkIcon className="w-4 h-4 text-brand-400 flex-shrink-0" />
            <h3 className="font-semibold text-white">Connection URLs</h3>
          </div>
          <p className="text-xs text-slate-500 mb-3">
            Use these endpoints with an API key to connect MCP clients to this compound.
          </p>
          <div className="space-y-2">
            <div className="flex items-center gap-2">
              <span className="flex-shrink-0 text-xs font-mono px-1.5 py-0.5 bg-brand-950/50 text-brand-400 rounded">
                POST
              </span>
              <code className="flex-1 text-xs text-slate-300 font-mono break-all">{mcpUrl}</code>
              {renderCopyButton(mcpUrl)}
            </div>
            <div className="flex items-center gap-2">
              <span className="flex-shrink-0 text-xs font-mono px-1.5 py-0.5 bg-emerald-950/50 text-emerald-400 rounded">
                SSE
              </span>
              <code className="flex-1 text-xs text-slate-300 font-mono break-all">{sseUrl}</code>
              {renderCopyButton(sseUrl)}
            </div>
          </div>
        </div>

        {/* Danger zone */}
        <div className="bg-slate-900 rounded-xl border border-slate-800 p-4 sm:p-5">
          <h3 className="font-semibold text-red-400 mb-2">Delete Compound</h3>
          <p className="text-sm text-slate-500 mb-3 break-words">
            Deleting this compound will not affect the member servers, but API keys scoped to it will
            lose their compound association.
          </p>
          <button
            onClick={() => {
              deleteMutation.mutate(selectedId);
              navigate('/compounds');
            }}
            className="flex items-center gap-2 px-4 py-2 min-h-[40px] bg-red-950/50 hover:bg-red-900/50 text-red-400 border border-red-900 rounded-lg text-sm font-medium transition-colors"
          >
            <Trash2 className="w-4 h-4 flex-shrink-0" />
            Delete Compound
          </button>
        </div>
      </div>
    );
  }

  // List view
  return (
    <div className="space-y-6 pb-20 lg:pb-0">
      {/* Header */}
      <div className="flex items-start sm:items-center justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-xl sm:text-2xl font-bold text-white">Compound Servers</h1>
          <p className="text-sm text-slate-500 mt-1">
            Group multiple MCP servers into a single logical endpoint
          </p>
        </div>
        <button
          onClick={() => setShowCreate(true)}
          className="flex-shrink-0 flex items-center gap-2 px-4 py-2 min-h-[40px] bg-brand-600 hover:bg-brand-700 text-white rounded-lg font-medium transition-colors"
        >
          <Plus className="w-4 h-4 flex-shrink-0" />
          <span className="hidden sm:inline">New Compound</span>
          <span className="sm:hidden">New</span>
        </button>
      </div>

      {showCreate && (
        <CreateCompoundForm
          servers={allServers || []}
          onClose={() => setShowCreate(false)}
          onSuccess={() => {
            setShowCreate(false);
            queryClient.invalidateQueries({ queryKey: ['compounds'] });
            queryClient.invalidateQueries({ queryKey: ['dashboard'] });
          }}
        />
      )}

      <div className="bg-slate-900 rounded-xl border border-slate-800">
        <div className="divide-y divide-slate-800">
          {compounds?.length === 0 && !showCreate && (
            <div className="px-5 py-12 text-center">
              <Layers className="w-10 h-10 text-slate-700 mx-auto mb-3" />
              <p className="text-sm text-slate-500">No compound servers yet</p>
              <p className="text-xs text-slate-600 mt-1">Create one to group multiple MCP servers</p>
            </div>
          )}
          {compounds?.map((c) => (
            <Link
              key={c.id}
              to={`/compounds/${c.id}`}
              className="px-4 sm:px-5 py-4 flex items-center justify-between gap-3 hover:bg-slate-800/50 transition-colors"
            >
              <div className="flex items-center gap-3 min-w-0">
                <div className="w-10 h-10 rounded-lg bg-brand-950/50 flex items-center justify-center flex-shrink-0">
                  <Layers className="w-5 h-5 text-brand-400" />
                </div>
                <div className="min-w-0">
                  <div className="font-medium text-white truncate">{c.name}</div>
                  {c.description && (
                    <div className="text-xs text-slate-500 truncate">{c.description}</div>
                  )}
                </div>
              </div>
              <div className="flex items-center gap-2 text-xs text-slate-500 flex-shrink-0">
                <span className="hidden sm:inline">{new Date(c.created_at).toLocaleDateString()}</span>
              </div>
            </Link>
          ))}
        </div>
      </div>
    </div>
  );
}

function CreateCompoundForm({
  servers,
  onClose,
  onSuccess,
}: {
  servers: { id: string; name: string; transport: string; status: string }[];
  onClose: () => void;
  onSuccess: () => void;
}) {
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  const toggleServer = (id: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError('');
    try {
      await compoundsApi.create({
        name,
        description,
        member_ids: Array.from(selectedIds),
      });
      onSuccess();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create compound');
    } finally {
      setLoading(false);
    }
  };

  return (
    <form
      onSubmit={handleSubmit}
      className="bg-slate-900 rounded-xl border border-slate-800 p-4 sm:p-6 space-y-4"
    >
      <h2 className="text-lg font-semibold text-white">Create Compound Server</h2>

      <div>
        <label className="block text-sm font-medium text-slate-300 mb-1.5">Name</label>
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          className="w-full px-3 py-2 min-h-[40px] bg-slate-800 border border-slate-700 rounded-lg text-white placeholder-slate-500 focus:outline-none focus:border-brand-500 transition-colors"
          placeholder="dev-tools"
          required
        />
      </div>

      <div>
        <label className="block text-sm font-medium text-slate-300 mb-1.5">
          Description (optional)
        </label>
        <input
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          className="w-full px-3 py-2 min-h-[40px] bg-slate-800 border border-slate-700 rounded-lg text-white placeholder-slate-500 focus:outline-none focus:border-brand-500 transition-colors"
          placeholder="Development tools group"
        />
      </div>

      <div>
        <label className="block text-sm font-medium text-slate-300 mb-1.5">
          Member Servers ({selectedIds.size} selected)
        </label>
        <div className="space-y-1 max-h-48 overflow-y-auto">
          {servers.length === 0 && (
            <p className="text-sm text-slate-500 px-3 py-4 text-center">
              No servers available. Add servers first.
            </p>
          )}
          {servers.map((s) => (
            <button
              key={s.id}
              type="button"
              onClick={() => toggleServer(s.id)}
              className={`w-full flex items-center justify-between gap-2 px-3 py-2 min-h-[40px] rounded-lg text-sm transition-colors ${
                selectedIds.has(s.id)
                  ? 'bg-brand-600/20 text-brand-300 border border-brand-800'
                  : 'bg-slate-800 text-slate-400 hover:bg-slate-700 border border-transparent'
              }`}
            >
              <div className="flex items-center gap-2 min-w-0">
                <Server className="w-4 h-4 flex-shrink-0" />
                <span className="font-medium truncate">{s.name}</span>
                <span className="text-xs text-slate-500 flex-shrink-0">{s.transport}</span>
              </div>
              {s.status === 'connected' ? (
                <CheckCircle2 className="w-4 h-4 text-emerald-400 flex-shrink-0" />
              ) : (
                <XCircle className="w-4 h-4 text-slate-600 flex-shrink-0" />
              )}
            </button>
          ))}
        </div>
      </div>

      {error && (
        <div className="text-sm text-red-400 bg-red-950/50 border border-red-900 rounded-lg px-3 py-2 break-words">
          {error}
        </div>
      )}

      <div className="flex gap-3">
        <button
          type="submit"
          disabled={loading}
          className="flex-1 sm:flex-initial px-4 py-2 min-h-[40px] bg-brand-600 hover:bg-brand-700 disabled:opacity-50 text-white rounded-lg font-medium transition-colors"
        >
          {loading ? 'Creating...' : 'Create Compound'}
        </button>
        <button
          type="button"
          onClick={onClose}
          className="flex-1 sm:flex-initial px-4 py-2 min-h-[40px] bg-slate-800 hover:bg-slate-700 text-slate-300 rounded-lg font-medium transition-colors"
        >
          Cancel
        </button>
      </div>
    </form>
  );
}
