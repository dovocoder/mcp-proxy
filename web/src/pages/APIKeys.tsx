import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Plus, Trash2, KeyRound, Copy, Check, AlertCircle, Layers } from 'lucide-react';
import { apiKeys as apiKeysApi, compounds as compoundsApi, type APIKeyWithSecret, type APIKey } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { ConfirmDialog } from '@/components/ConfirmDialog';
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from '@/components/ui/select';
import {
  Dialog,
  DialogTrigger,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  DialogClose,
} from '@/components/ui/dialog';

const ALL_SCOPES = ['read', 'write', 'admin'] as const;
const NONE_COMPOUND = '__none__';

export default function APIKeys() {
  const queryClient = useQueryClient();
  const { data: keys } = useQuery({ queryKey: ['apiKeys'], queryFn: apiKeysApi.list });
  const { data: compounds } = useQuery({ queryKey: ['compounds'], queryFn: compoundsApi.list });
  const [showForm, setShowForm] = useState(false);
  const [newKey, setNewKey] = useState<APIKeyWithSecret | null>(null);
  const [copied, setCopied] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<APIKey | null>(null);

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
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-start sm:items-center justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-xl sm:text-2xl font-bold text-foreground">API Keys</h1>
          <p className="text-sm text-muted-foreground mt-1">Manage authentication keys for MCP clients</p>
        </div>
        <Dialog open={showForm} onOpenChange={setShowForm}>
          <DialogTrigger render={
            <Button className="shrink-0">
              <Plus />
              <span className="hidden sm:inline">Generate Key</span>
              <span className="sm:hidden">Generate</span>
            </Button>
          } />
          <DialogContent className="sm:max-w-md">
            <KeyForm
              compounds={compounds || []}
              onClose={() => setShowForm(false)}
              onSuccess={(key) => {
                setNewKey(key);
                setShowForm(false);
                queryClient.invalidateQueries({ queryKey: ['apiKeys'] });
              }}
            />
          </DialogContent>
        </Dialog>
      </div>

      {/* New key banner */}
      {newKey && (
        <Card className="ring-destructive/20 bg-destructive/5">
          <CardContent className="space-y-3">
            <div className="flex items-start gap-3">
              <AlertCircle className="size-5 text-destructive shrink-0 mt-0.5" />
              <div className="flex-1 min-w-0">
                <h3 className="font-semibold text-foreground mb-1">API Key Created</h3>
                <p className="text-sm text-muted-foreground mb-3">
                  Save this key — it will not be shown again.
                </p>
                <div className="flex items-center gap-2">
                  <code className="flex-1 min-w-0 bg-muted rounded-lg px-3 py-2 font-mono text-sm break-all text-foreground">
                    {newKey.key}
                  </code>
                  <Button
                    variant="outline"
                    size="icon"
                    onClick={handleCopy}
                    aria-label="Copy key"
                    className="shrink-0"
                  >
                    {copied ? <Check /> : <Copy />}
                  </Button>
                </div>
              </div>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setNewKey(null)}
                className="shrink-0"
              >
                Dismiss
              </Button>
            </div>
          </CardContent>
        </Card>
      )}

      {/* Keys list */}
      <Card>
        <CardHeader className="border-b">
          <CardTitle>Your API Keys</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          {keys?.length === 0 && !showForm ? (
            <div className="px-5 py-12 text-center">
              <KeyRound className="size-10 text-muted-foreground/50 mx-auto mb-3" />
              <p className="text-sm text-muted-foreground">No API keys generated yet</p>
            </div>
          ) : (
            <div className="divide-y divide-border">
              {keys?.map((key) => (
                <div
                  key={key.id}
                  className="px-4 sm:px-5 py-4 flex flex-col sm:flex-row sm:items-center justify-between gap-3"
                >
                  <div className="flex items-center gap-3 min-w-0">
                    <KeyRound className="size-4 text-muted-foreground shrink-0" />
                    <div className="min-w-0">
                      <div className="font-medium text-foreground truncate">{key.name}</div>
                      <div className="text-xs text-muted-foreground font-mono flex flex-wrap items-center gap-x-2 gap-y-1">
                        <span className="truncate">{key.key_prefix}</span>
                        <span>· scopes: {key.scopes.join(', ') || 'none'}</span>
                        {key.compound_id && (
                          <Badge variant="secondary" className="gap-1">
                            <Layers className="size-3" />
                            {compounds?.find((c) => c.id === key.compound_id)?.name || 'compound'}
                          </Badge>
                        )}
                      </div>
                    </div>
                  </div>
                  <div className="flex items-center gap-3 flex-wrap">
                    {key.last_used_at && (
                      <span className="text-xs text-muted-foreground">
                        Last used: {new Date(key.last_used_at).toLocaleDateString()}
                      </span>
                    )}
                    {key.expires_at && (
                      <span className="text-xs text-amber-500">
                        Expires: {new Date(key.expires_at).toLocaleDateString()}
                      </span>
                    )}
                    <Badge variant={key.active ? 'secondary' : 'outline'}>
                      {key.active ? 'active' : 'inactive'}
                    </Badge>
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() => setDeleteTarget(key)}
                      aria-label="Delete key"
                      className="text-muted-foreground hover:text-destructive"
                    >
                      <Trash2 />
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Delete API Key"
        description="Are you sure you want to delete"
        itemName={deleteTarget?.name}
        confirmText="Delete Key"
        loading={deleteMutation.isPending}
        onConfirm={() => {
          if (deleteTarget) {
            deleteMutation.mutate(deleteTarget.id, {
              onSuccess: () => setDeleteTarget(null),
            });
          }
        }}
      />
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
    <form onSubmit={handleSubmit} className="space-y-4">
      <DialogHeader>
        <DialogTitle>Generate API Key</DialogTitle>
        <DialogDescription>Create a new authentication key for MCP clients.</DialogDescription>
      </DialogHeader>

      <div className="space-y-2">
        <Label htmlFor="key-name">Name</Label>
        <Input
          id="key-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Production CI"
          required
        />
      </div>

      <div className="space-y-2">
        <Label>Scopes</Label>
        <div className="flex flex-wrap gap-2">
          {ALL_SCOPES.map((scope) => (
            <Button
              key={scope}
              type="button"
              variant={scopes.includes(scope) ? 'default' : 'outline'}
              size="sm"
              onClick={() => toggleScope(scope)}
            >
              {scope}
            </Button>
          ))}
        </div>
      </div>

      {compounds.length > 0 && (
        <div className="space-y-2">
          <Label>
            Compound Server{' '}
            <span className="text-muted-foreground font-normal">
              (scope key to specific compound)
            </span>
          </Label>
          <Select
            value={compoundId === '' ? NONE_COMPOUND : compoundId}
            onValueChange={(val) => setCompoundId(val === NONE_COMPOUND ? '' : (val ?? ''))}
          >
            <SelectTrigger className="w-full">
              <SelectValue placeholder="All servers (global)" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={NONE_COMPOUND}>All servers (global)</SelectItem>
              {compounds.map((c) => (
                <SelectItem key={c.id} value={c.id}>
                  {c.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      )}

      <div className="space-y-2">
        <Label htmlFor="key-expiry">Expires in (days, optional)</Label>
        <Input
          id="key-expiry"
          type="number"
          value={expiresIn}
          onChange={(e) => setExpiresIn(e.target.value)}
          placeholder="90"
        />
      </div>

      {error && (
        <p className="text-sm text-destructive bg-destructive/10 border border-destructive/30 rounded-lg px-3 py-2 break-words">
          {error}
        </p>
      )}

      <DialogFooter>
        <DialogClose render={<Button type="button" variant="outline">Cancel</Button>} />
        <Button type="submit" disabled={loading}>
          {loading ? 'Generating...' : 'Generate'}
        </Button>
      </DialogFooter>
    </form>
  );
}
