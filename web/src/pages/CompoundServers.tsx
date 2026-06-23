import { useState } from 'react';
import { useParams, Link, useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  Plus,
  Trash2,
  Layers,
  ArrowLeft,
  Server as ServerIcon,
  CheckCircle2,
  XCircle,
  X,
  Link as LinkIcon,
  Copy,
  Check,
} from 'lucide-react';
import { compounds as compoundsApi, servers as serversApi, type Server } from '@/api/client';
import { Button, buttonVariants } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { Card, CardContent, CardHeader, CardTitle, CardDescription, CardAction } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Separator } from '@/components/ui/separator';
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

export default function CompoundServers() {
  const { id: selectedId } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [showCreate, setShowCreate] = useState(false);
  const [copiedUrl, setCopiedUrl] = useState<string | null>(null);
  const [addServerId, setAddServerId] = useState<string | null>(null);

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

  // --- Detail view ---
  if (selectedId && detail) {
    const memberIds = new Set(detail.members.map((m) => m.id));
    const availableServers = allServers?.filter((s) => !memberIds.has(s.id)) || [];

    const mcpUrl = `/api/compounds/${selectedId}/mcp`;
    const sseUrl = `/api/compounds/${selectedId}/sse`;

    return (
      <div className="space-y-6 pb-20 lg:pb-0">
        {/* Header */}
        <div className="flex items-start gap-3 sm:gap-4">
          <Link to="/compounds" className="shrink-0">
            <Button variant="ghost" size="icon">
              <ArrowLeft />
            </Button>
          </Link>
          <div className="flex-1 min-w-0">
            <h1 className="text-xl sm:text-2xl font-bold text-foreground truncate">{detail.name}</h1>
            {detail.description && (
              <p className="text-sm text-muted-foreground mt-1 break-words">{detail.description}</p>
            )}
          </div>
          <div className="flex items-center gap-4 sm:gap-6 shrink-0">
            <div className="text-right">
              <div className="text-xl sm:text-2xl font-bold text-primary">{detail.tool_count}</div>
              <div className="text-xs text-muted-foreground">tools</div>
            </div>
            <div className="text-right">
              <div className="text-xl sm:text-2xl font-bold text-foreground">{detail.members.length}</div>
              <div className="text-xs text-muted-foreground">members</div>
            </div>
          </div>
        </div>

        {/* Member servers */}
        <Card>
          <CardHeader className="border-b">
            <CardTitle>Member Servers</CardTitle>
            <CardAction>
              {availableServers.length > 0 && (
                <Select
                  value={addServerId}
                  onValueChange={(val) => {
                    if (val) {
                      addMemberMutation.mutate({ compoundId: selectedId, serverId: String(val) });
                      setAddServerId(null);
                    }
                  }}
                >
                  <SelectTrigger className="w-full sm:w-auto">
                    <SelectValue placeholder="+ Add server" />
                  </SelectTrigger>
                  <SelectContent>
                    {availableServers.map((s) => (
                      <SelectItem key={s.id} value={s.id}>
                        {s.name} ({s.transport})
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </CardAction>
          </CardHeader>
          <CardContent className="p-0">
            {detail.members.length === 0 ? (
              <div className="px-5 py-8 text-center text-sm text-muted-foreground">
                No members yet. Add servers to this compound.
              </div>
            ) : (
              <div className="divide-y divide-border">
                {detail.members.map((m) => (
                  <div key={m.id} className="px-4 sm:px-5 py-3 flex items-center justify-between gap-3">
                    <div className="flex items-center gap-3 min-w-0">
                      <ServerIcon className="size-4 text-muted-foreground shrink-0" />
                      <div className="min-w-0">
                        <div className="font-medium text-foreground truncate">{m.name}</div>
                        <div className="text-xs text-muted-foreground">
                          {m.transport} · {m.status}
                        </div>
                      </div>
                    </div>
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      onClick={() =>
                        removeMemberMutation.mutate({ compoundId: selectedId, serverId: m.id })
                      }
                      aria-label="Remove member"
                      className="text-muted-foreground hover:text-destructive shrink-0"
                    >
                      <X />
                    </Button>
                  </div>
                ))}
              </div>
            )}
          </CardContent>
        </Card>

        {/* Connection URLs */}
        <Card>
          <CardHeader>
            <div className="flex items-center gap-2">
              <LinkIcon className="size-4 text-primary" />
              <CardTitle>Connection URLs</CardTitle>
            </div>
            <CardDescription>
              Use these endpoints with an API key to connect MCP clients to this compound.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="flex items-center gap-2">
              <Badge variant="default" className="font-mono shrink-0">POST</Badge>
              <code className="flex-1 min-w-0 text-xs text-muted-foreground font-mono break-all">
                {mcpUrl}
              </code>
              <Button
                variant="outline"
                size="icon-sm"
                onClick={() => copyToClipboard(mcpUrl)}
                aria-label="Copy URL"
                className="shrink-0"
              >
                {copiedUrl === mcpUrl ? <Check /> : <Copy />}
              </Button>
            </div>
            <Separator />
            <div className="flex items-center gap-2">
              <Badge variant="secondary" className="font-mono shrink-0">SSE</Badge>
              <code className="flex-1 min-w-0 text-xs text-muted-foreground font-mono break-all">
                {sseUrl}
              </code>
              <Button
                variant="outline"
                size="icon-sm"
                onClick={() => copyToClipboard(sseUrl)}
                aria-label="Copy URL"
                className="shrink-0"
              >
                {copiedUrl === sseUrl ? <Check /> : <Copy />}
              </Button>
            </div>
          </CardContent>
        </Card>

        {/* Danger zone */}
        <Card className="ring-destructive/20">
          <CardHeader>
            <CardTitle className="text-destructive">Delete Compound</CardTitle>
            <CardDescription>
              Deleting this compound will not affect the member servers, but API keys scoped to it will
              lose their compound association.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Button
              variant="destructive"
              onClick={() => {
                deleteMutation.mutate(selectedId);
                navigate('/compounds');
              }}
            >
              <Trash2 />
              Delete Compound
            </Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  // --- List view ---
  return (
    <div className="space-y-6 pb-20 lg:pb-0">
      {/* Header */}
      <div className="flex items-start sm:items-center justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-xl sm:text-2xl font-bold text-foreground">Compound Servers</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Group multiple MCP servers into a single logical endpoint
          </p>
        </div>
        <Dialog open={showCreate} onOpenChange={setShowCreate}>
          <DialogTrigger render={
            <Button className="shrink-0">
              <Plus />
              <span className="hidden sm:inline">New Compound</span>
              <span className="sm:hidden">New</span>
            </Button>
          } />
          <DialogContent className="sm:max-w-md">
            <CreateCompoundForm
              servers={allServers || []}
              onClose={() => setShowCreate(false)}
              onSuccess={() => {
                setShowCreate(false);
                queryClient.invalidateQueries({ queryKey: ['compounds'] });
                queryClient.invalidateQueries({ queryKey: ['dashboard'] });
              }}
            />
          </DialogContent>
        </Dialog>
      </div>

      {/* Compound list */}
      {compounds?.length === 0 && !showCreate ? (
        <Card>
          <CardContent className="py-12 text-center">
            <Layers className="size-10 text-muted-foreground/50 mx-auto mb-3" />
            <p className="text-sm text-muted-foreground">No compound servers yet</p>
            <p className="text-xs text-muted-foreground/70 mt-1">Create one to group multiple MCP servers</p>
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-3">
          {compounds?.map((c) => (
            <Card key={c.id}>
              <CardContent className="py-4">
                <Link to={`/compounds/${c.id}`} className="flex items-center justify-between gap-3">
                  <div className="flex items-center gap-3 min-w-0">
                    <div className="size-10 rounded-lg bg-primary/10 flex items-center justify-center shrink-0">
                      <Layers className="size-5 text-primary" />
                    </div>
                    <div className="min-w-0">
                      <div className="font-medium text-foreground truncate">{c.name}</div>
                      {c.description && (
                        <div className="text-xs text-muted-foreground truncate">{c.description}</div>
                      )}
                    </div>
                  </div>
                  <div className="flex items-center gap-2 text-xs text-muted-foreground shrink-0">
                    <span className="hidden sm:inline">{new Date(c.created_at).toLocaleDateString()}</span>
                  </div>
                </Link>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}

function CreateCompoundForm({
  servers,
  onClose,
  onSuccess,
}: {
  servers: Server[];
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
    <form onSubmit={handleSubmit} className="space-y-4">
      <DialogHeader>
        <DialogTitle>Create Compound Server</DialogTitle>
        <DialogDescription>Group multiple MCP servers into a single logical endpoint.</DialogDescription>
      </DialogHeader>

      <div className="space-y-2">
        <Label htmlFor="compound-name">Name</Label>
        <Input
          id="compound-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="dev-tools"
          required
        />
      </div>

      <div className="space-y-2">
        <Label htmlFor="compound-description">
          Description <span className="text-muted-foreground font-normal">(optional)</span>
        </Label>
        <Input
          id="compound-description"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="Development tools group"
        />
      </div>

      <div className="space-y-2">
        <Label>
          Member Servers{' '}
          <span className="text-muted-foreground font-normal">({selectedIds.size} selected)</span>
        </Label>
        <div className="space-y-1 max-h-48 overflow-y-auto">
          {servers.length === 0 && (
            <p className="text-sm text-muted-foreground px-3 py-4 text-center">
              No servers available. Add servers first.
            </p>
          )}
          {servers.map((s) => {
            const selected = selectedIds.has(s.id);
            return (
              <button
                key={s.id}
                type="button"
                onClick={() => toggleServer(s.id)}
                className={`w-full flex items-center justify-between gap-2 px-3 py-2 min-h-[40px] rounded-lg text-sm transition-colors border ${
                  selected
                    ? 'bg-primary/10 text-foreground border-primary/30'
                    : 'bg-transparent text-muted-foreground hover:bg-muted border-border'
                }`}
              >
                <div className="flex items-center gap-2 min-w-0">
                  <ServerIcon className="size-4 shrink-0" />
                  <span className="font-medium truncate">{s.name}</span>
                  <span className="text-xs shrink-0">{s.transport}</span>
                </div>
                {s.status === 'connected' ? (
                  <CheckCircle2 className="size-4 text-primary shrink-0" />
                ) : (
                  <XCircle className="size-4 text-muted-foreground/50 shrink-0" />
                )}
              </button>
            );
          })}
        </div>
      </div>

      {error && (
        <p className="text-sm text-destructive bg-destructive/10 border border-destructive/30 rounded-lg px-3 py-2 break-words">
          {error}
        </p>
      )}

      <DialogFooter>
        <DialogClose render={<Button type="button" variant="outline">Cancel</Button>} />
        <Button type="submit" disabled={loading}>
          {loading ? 'Creating...' : 'Create Compound'}
        </Button>
      </DialogFooter>
    </form>
  );
}
