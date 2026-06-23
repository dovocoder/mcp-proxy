import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Plus, Trash2, KeyRound, Copy, Check, AlertCircle, Layers } from 'lucide-react';
import { apiKeys as apiKeysApi, compounds as compoundsApi, type APIKeyWithSecret } from '../api/client';

export default function APIKeys() {
  const queryClient = useQueryClient();
  const { data: keys } = useQuery({ queryKey: ['apiKeys'], queryFn: apiKeysApi.list });
  const { data: compounds } = useQuery({ queryKey: ['compounds'], queryFn: compoundsApi.list });
  const [showForm, setShowForm] = useState(false);
  const [newKey, setNewKey] = useState<APIKeyWithSecret | null>(null);
  const [copied, setCopied] = useState(false);

  const deleteMutation = useMutation({
    mutationFn: apiKeysApi.delete,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['apiKeys'] }),
  });

  const handleCopy = () => {
    if (newKey) {
      navigator.clipboard.writeText(newKey.key);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  return (
    <div className="space-y-6 pb-20 lg:pb-0">
      {/* Header */}
      <div className="flex items-start sm:items-center justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-xl sm:text-2xl font-bold text-white">API Keys</h1>
          <p className="text-sm text-slate-500 mt-1">Manage authentication keys for MCP clients</p>
        </div>
        <button
          onClick={() => setShowForm(true)}
          className="flex-shrink-0 flex items-center gap-2 px-4 py-2 min-h-[40px] bg-brand-600 hover:bg-brand-700 text-white rounded-lg font-medium transition-colors"
        >
          <Plus className="w-4 h-4 flex-shrink-0" />
          <span className="hidden sm:inline">Generate Key</span>
          <span className="sm:hidden">Generate</span>
        </button>
      </div>

      {/* New key banner */}
      {newKey && (
        <div className="bg-emerald-950/30 border border-emerald-900 rounded-xl p-4 sm:p-5">
          <div className="flex items-start gap-3">
            <AlertCircle className="w-5 h-5 text-emerald-400 flex-shrink-0 mt-0.5" />
            <div className="flex-1 min-w-0">
              <h3 className="font-semibold text-emerald-300 mb-1">API Key Created</h3>
              <p className="text-sm text-emerald-500/70 mb-3">
                Copy this key now — it will not be shown again.
              </p>
              <div className="flex items-center gap-2">
                <code className="flex-1 min-w-0 bg-slate-900 text-emerald-300 rounded-lg px-4 py-2.5 font-mono text-sm break-all">
                  {newKey.key}
                </code>
                <button
                  onClick={handleCopy}
                  className="flex-shrink-0 p-2.5 min-h-[40px] min-w-[40px] flex items-center justify-center bg-slate-800 hover:bg-slate-700 text-slate-300 rounded-lg transition-colors"
                  aria-label="Copy key"
                >
                  {copied ? (
                    <Check className="w-4 h-4 text-emerald-400" />
                  ) : (
                    <Copy className="w-4 h-4" />
                  )}
                </button>
              </div>
            </div>
            <button
              onClick={() => setNewKey(null)}
              className="flex-shrink-0 text-emerald-500/70 hover:text-emerald-300 text-sm min-h-[40px] px-2"
            >
              Dismiss
            </button>
          </div>
        </div>
      )}

      {showForm && (
        <KeyForm
          compounds={compounds || []}
          onClose={() => setShowForm(false)}
          onSuccess={(key) => {
            setNewKey(key);
            setShowForm(false);
            queryClient.invalidateQueries({ queryKey: ['apiKeys'] });
          }}
        />
      )}

      {/* Keys list */}
      <div className="bg-slate-900 rounded-xl border border-slate-800">
        <div className="divide-y divide-slate-800">
          {keys?.length === 0 && !showForm && (
            <div className="px-5 py-12 text-center">
              <KeyRound className="w-10 h-10 text-slate-700 mx-auto mb-3" />
              <p className="text-sm text-slate-500">No API keys generated yet</p>
            </div>
          )}
          {keys?.map((key) => (
            <div
              key={key.id}
              className="px-4 sm:px-5 py-4 flex flex-col sm:flex-row sm:items-center justify-between gap-3"
            >
              <div className="flex items-center gap-3 min-w-0">
                <KeyRound className="w-4 h-4 text-slate-500 flex-shrink-0" />
                <div className="min-w-0">
                  <div className="font-medium text-white truncate">{key.name}</div>
                  <div className="text-xs text-slate-500 font-mono flex flex-wrap items-center gap-x-2 gap-y-1">
                    <span className="truncate">{key.key_prefix}</span>
                    <span>· scopes: {key.scopes.join(', ') || 'none'}</span>
                    {key.compound_id && (
                      <span className="inline-flex items-center gap-1 text-cyan-400">
                        <Layers className="w-3 h-3 flex-shrink-0" />
                        {compounds?.find((c) => c.id === key.compound_id)?.name || 'compound'}
                      </span>
                    )}
                  </div>
                </div>
              </div>
              <div className="flex items-center gap-3 flex-wrap">
                {key.last_used_at && (
                  <span className="text-xs text-slate-500">
                    Last used: {new Date(key.last_used_at).toLocaleDateString()}
                  </span>
                )}
                {key.expires_at && (
                  <span className="text-xs text-amber-400">
                    Expires: {new Date(key.expires_at).toLocaleDateString()}
                  </span>
                )}
                <span
                  className={`text-xs px-2 py-0.5 rounded-full ${
                    key.active ? 'bg-emerald-950/50 text-emerald-400' : 'bg-slate-800 text-slate-500'
                  }`}
                >
                  {key.active ? 'active' : 'inactive'}
                </span>
                <button
                  onClick={() => deleteMutation.mutate(key.id)}
                  className="flex-shrink-0 p-2 min-h-[40px] min-w-[40px] flex items-center justify-center text-slate-400 hover:text-red-400 hover:bg-slate-800 rounded-lg transition-colors"
                  aria-label="Delete key"
                >
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

function KeyForm({
  compounds,
  onClose,
  onSuccess,
}: {
  compounds: { id: string; name: string }[];
  onClose: () => void;
  onSuccess: (key: APIKeyWithSecret) => void;
}) {
  const [name, setName] = useState('');
  const [scopes, setScopes] = useState<string[]>(['read', 'write']);
  const [compoundId, setCompoundId] = useState('');
  const [expiresIn, setExpiresIn] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  const allScopes = ['read', 'write', 'admin'];

  const toggleScope = (scope: string) => {
    setScopes((prev) =>
      prev.includes(scope) ? prev.filter((s) => s !== scope) : [...prev, scope]
    );
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError('');
    try {
      const data: {
        name: string;
        scopes: string[];
        compound_id?: string;
        expires_in_days?: number;
      } = { name, scopes };
      if (compoundId) data.compound_id = compoundId;
      if (expiresIn) data.expires_in_days = parseInt(expiresIn);
      const key = await apiKeysApi.create(data);
      onSuccess(key);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create key');
    } finally {
      setLoading(false);
    }
  };

  return (
    <form
      onSubmit={handleSubmit}
      className="bg-slate-900 rounded-xl border border-slate-800 p-4 sm:p-6 space-y-4"
    >
      <h2 className="text-lg font-semibold text-white">Generate API Key</h2>

      <div>
        <label className="block text-sm font-medium text-slate-300 mb-1.5">Name</label>
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          className="w-full px-3 py-2 min-h-[40px] bg-slate-800 border border-slate-700 rounded-lg text-white placeholder-slate-500 focus:outline-none focus:border-brand-500 transition-colors"
          placeholder="Production CI"
          required
        />
      </div>

      <div>
        <label className="block text-sm font-medium text-slate-300 mb-1.5">Scopes</label>
        <div className="flex flex-wrap gap-2">
          {allScopes.map((scope) => (
            <button
              key={scope}
              type="button"
              onClick={() => toggleScope(scope)}
              className={`px-3 py-2 min-h-[40px] rounded-lg text-sm font-medium transition-colors ${
                scopes.includes(scope)
                  ? 'bg-brand-600 text-white'
                  : 'bg-slate-800 text-slate-400 hover:bg-slate-700'
              }`}
            >
              {scope}
            </button>
          ))}
        </div>
      </div>

      {compounds.length > 0 && (
        <div>
          <label className="block text-sm font-medium text-slate-300 mb-1.5">
            Compound Server{' '}
            <span className="text-slate-500 font-normal">(scope key to specific compound)</span>
          </label>
          <select
            value={compoundId}
            onChange={(e) => setCompoundId(e.target.value)}
            className="w-full px-3 py-2 min-h-[40px] bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-brand-500 transition-colors"
          >
            <option value="">All servers (global)</option>
            {compounds.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </div>
      )}

      <div>
        <label className="block text-sm font-medium text-slate-300 mb-1.5">
          Expires in (days, optional)
        </label>
        <input
          type="number"
          value={expiresIn}
          onChange={(e) => setExpiresIn(e.target.value)}
          className="w-full px-3 py-2 min-h-[40px] bg-slate-800 border border-slate-700 rounded-lg text-white placeholder-slate-500 focus:outline-none focus:border-brand-500 transition-colors"
          placeholder="90"
        />
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
          {loading ? 'Generating...' : 'Generate'}
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
