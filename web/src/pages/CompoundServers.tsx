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
  Ban,
  Wrench,
  Check,
  Brain,
} from 'lucide-react';
import {
  compounds as compoundsApi,
  servers as serversApi,
  tools as toolsApi,
  disabledTools as disabledToolsApi,
  type Server,
  type CompoundServerWithMembers,
} from '@/api/client';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle, CardDescription, CardAction } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Separator } from '@/components/ui/separator';
import { Switch } from '@/components/ui/switch';
import { ConfirmDialog } from '@/components/ConfirmDialog';
import { InfoBanner } from '@/components/InfoBanner';
import { CollapsibleConnectionURLs } from '@/components/CollapsibleConnectionURLs';
import { cn } from '@/lib/utils';
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
  const [addServerId, setAddServerId] = useState<string | null>(null);
  const [deleteCompoundOpen, setDeleteCompoundOpen] = useState(false);
  const [removeMemberTarget, setRemoveMemberTarget] = useState<Server | null>(null);

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

  const updateMutation = useMutation({
    mutationFn: ({ compoundId, data }: { compoundId: string; data: { dictionary_mode?: boolean } }) =>
      compoundsApi.update(compoundId, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['compound', selectedId] });
      queryClient.invalidateQueries({ queryKey: ['compounds'] });
    },
  });

  const [newDisabledTool, setNewDisabledTool] = useState('');

  const { data: allTools } = useQuery({ queryKey: ['tools'], queryFn: toolsApi.list });

  const { data: compoundDisabledTools } = useQuery({
    queryKey: ['disabled-tools', selectedId],
    queryFn: () => disabledToolsApi.list(selectedId!),
    enabled: !!selectedId,
  });

  const disableToolMutation = useMutation({
    mutationFn: ({ toolName, compoundId }: { toolName: string; compoundId: string }) =>
      disabledToolsApi.create({ tool_name: toolName, compound_id: compoundId }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['disabled-tools', selectedId] });
      setNewDisabledTool('');
    },
  });

  const enableToolMutation = useMutation({
    mutationFn: (id: string) => disabledToolsApi.delete(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['disabled-tools', selectedId] });
    },
  });

  // --- Detail view ---
  if (selectedId && detail) {
    const memberIds = new Set(detail.members.map((m) => m.id));
    const availableServers = allServers?.filter((s) => !memberIds.has(s.id)) || [];

    // Namespaced tool names available in this compound (for autocomplete)
    const compoundToolNames = (allTools || [])
      .filter((t) => memberIds.has(t.server_id))
      .map((t) => `${t.server_name}__${t.name}`);

    const origin = window.location.origin;
    const mcpUrl = `${origin}/api/compounds/${selectedId}/mcp`;
    const sseUrl = `${origin}/api/compounds/${selectedId}/sse`;

    return (
      <div className="space-y-5">
        {/* Header */}
        <div className="flex items-start gap-3 sm:gap-4">
          <Link to="/compounds" className="shrink-0">
            <Button variant="ghost" size="icon">
              <ArrowLeft />
            </Button>
          </Link>
          <div className="flex-1 min-w-0">
            <h1 className="text-xl sm:text-2xl font-bold text-foreground truncate">{detail.name}</h1>
            <p className="text-sm text-muted-foreground mt-0.5">
              {detail.members.length} member{detail.members.length !== 1 ? 's' : ''} · {detail.tool_count} tool{detail.tool_count !== 1 ? 's' : ''}
              {detail.dictionary_mode && ' · dictionary mode'}
            </p>
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
            <div className="flex items-center gap-2">
              <ServerIcon className="size-4 text-muted-foreground" />
              <div>
                <CardTitle>Member Servers</CardTitle>
                <CardDescription>
                  MCP servers included in this compound — their tools are combined into one endpoint
                </CardDescription>
              </div>
            </div>
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
                <ServerIcon className="size-8 text-muted-foreground/40 mx-auto mb-2" />
                No members yet. Add servers above to combine their tools.
              </div>
            ) : (
              <div className="divide-y divide-border">
                {detail.members.map((m) => (
                  <div key={m.id} className="px-4 sm:px-5 py-3 flex items-center justify-between gap-3">
                    <div className="flex items-center gap-3 min-w-0">
                      <div className={cn(
                        'inline-flex items-center justify-center w-8 h-8 rounded-lg shrink-0',
                        m.is_builtin ? 'bg-violet-500/10' : 'bg-muted'
                      )}>
                        {m.is_builtin ? <Brain className="size-4 text-violet-400" /> : <ServerIcon className="size-4 text-muted-foreground" />}
                      </div>
                      <div className="min-w-0">
                        <div className="flex items-center gap-2">
                          <span className="font-medium text-foreground truncate">{m.name}</span>
                          {m.is_builtin && <Badge variant="secondary" className="text-[10px]">builtin</Badge>}
                          {m.status === 'connected' ? (
                            <Badge variant="outline" className="text-[10px] border-emerald-500/30 text-emerald-400 bg-emerald-500/10">connected</Badge>
                          ) : (
                            <Badge variant="outline" className="text-[10px] text-muted-foreground">{m.status}</Badge>
                          )}
                        </div>
                        <div className="text-xs text-muted-foreground">
                          {m.transport}
                        </div>
                      </div>
                    </div>
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      onClick={() => setRemoveMemberTarget(m)}
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
        <CollapsibleConnectionURLs
          mcpUrl={mcpUrl}
          sseUrl={sseUrl}
          label="MCP Connection URLs"
          description="Use with an API key"
        />

        {/* Dictionary mode */}
        <Card>
          <CardHeader className="border-b">
            <div className="flex items-center gap-2">
              <Wrench className="size-4 text-muted-foreground shrink-0" />
              <div>
                <CardTitle>Dictionary Mode</CardTitle>
                <CardDescription>
                  Controls how tools are exposed to clients
                </CardDescription>
              </div>
            </div>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex items-center justify-between gap-4">
              <div className="text-sm text-muted-foreground">
                {detail.dictionary_mode ? (
                  <span>
                    <Badge variant="default" className="mr-1.5 text-[10px]">ON</Badge>
                    Clients see <strong className="text-foreground">1 tool</strong> (dictionary) that lists all {detail.tool_count} available tools.
                    Agents browse and call tools on demand.
                  </span>
                ) : (
                  <span>
                    <Badge variant="outline" className="mr-1.5 text-[10px]">OFF</Badge>
                    All <strong className="text-foreground">{detail.tool_count}</strong> tools are listed directly — clients see them all immediately.
                  </span>
                )}
              </div>
              <Switch
                checked={detail.dictionary_mode}
                onCheckedChange={(checked) =>
                  updateMutation.mutate({ compoundId: selectedId, data: { dictionary_mode: checked } })
                }
              />
            </div>

            {/* Tool breakdown */}
            <div className="flex flex-wrap gap-2 text-xs">
              <Badge variant="secondary">
                {detail.server_tool_count} server tool{detail.server_tool_count !== 1 ? 's' : ''}
              </Badge>
              {detail.memory_tool_count > 0 && (
                <Badge variant="secondary">
                  {detail.memory_tool_count} memory tool{detail.memory_tool_count !== 1 ? 's' : ''}
                </Badge>
              )}
              <Badge variant="outline">
                {detail.members.length} member{detail.members.length !== 1 ? 's' : ''}
              </Badge>
            </div>

            {/* How it works */}
            {detail.dictionary_mode && (
              <div className="rounded-lg border border-border bg-muted/30 p-3 space-y-2">
                <p className="text-xs font-medium text-foreground">How agents use the dictionary:</p>
                <ol className="text-xs text-muted-foreground space-y-1 list-decimal list-inside">
                  <li><code className="text-foreground">list</code> — get a lightweight catalog of all available tools (name + description + type)</li>
                  <li><code className="text-foreground">describe</code> — get the full input schema for a specific tool before calling it</li>
                  <li><code className="text-foreground">call</code> — execute a tool with arguments matching its schema</li>
                  <li><code className="text-foreground">search</code> — find tools by keyword when unsure what's available</li>
                </ol>
                <p className="text-xs text-muted-foreground pt-1 border-t border-border">
                  Tool names use <code className="text-foreground">serverName__toolName</code> format.
                  Memory tools use <code className="text-foreground">memory__toolName</code>.
                </p>
              </div>
            )}
          </CardContent>
        </Card>

        {/* Disabled Tools */}
        <Card>
          <CardHeader className="border-b">
            <div className="flex items-center gap-2">
              <Ban className="size-4 text-muted-foreground shrink-0" />
              <div>
                <CardTitle>Disabled Tools</CardTitle>
                <CardDescription>
                  Hide specific tools from clients accessing this compound. Tool names use{' '}
                  <code className="text-foreground">serverName__toolName</code> format.
                </CardDescription>
              </div>
            </div>
          </CardHeader>
          <CardContent className="space-y-4 pt-4">
            {/* Add new disabled tool */}
            <div className="flex gap-2">
              <Input
                list="compound-tool-names"
                value={newDisabledTool}
                onChange={(e) => setNewDisabledTool(e.target.value)}
                placeholder="serverName__toolName"
                className="font-mono text-sm"
              />
              <datalist id="compound-tool-names">
                {compoundToolNames.map((name) => (
                  <option key={name} value={name} />
                ))}
              </datalist>
              <Button
                onClick={() => {
                  if (newDisabledTool.trim()) {
                    disableToolMutation.mutate({
                      toolName: newDisabledTool.trim(),
                      compoundId: selectedId,
                    });
                  }
                }}
                disabled={!newDisabledTool.trim() || disableToolMutation.isPending}
                className="shrink-0"
              >
                <Ban className="size-4" />
                <span className="hidden sm:inline">Disable</span>
              </Button>
            </div>

            {/* List of disabled tools */}
            {(compoundDisabledTools || []).length === 0 ? (
              <p className="text-sm text-muted-foreground text-center py-4">
                No tools disabled for this compound
              </p>
            ) : (
              <div className="space-y-2">
                {(compoundDisabledTools || []).map((dt) => (
                  <div
                    key={dt.id}
                    className="flex items-center justify-between gap-2 rounded-lg bg-muted px-4 py-2.5"
                  >
                    <div className="flex items-center gap-2 min-w-0">
                      <Ban className="size-3.5 text-destructive shrink-0" />
                      <code className="text-sm text-foreground font-mono truncate">
                        {dt.tool_name}
                      </code>
                    </div>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => enableToolMutation.mutate(dt.id)}
                      disabled={enableToolMutation.isPending}
                      className="shrink-0"
                    >
                      <Check className="size-3.5" />
                      <span className="hidden sm:inline">Enable</span>
                    </Button>
                  </div>
                ))}
              </div>
            )}
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
              onClick={() => setDeleteCompoundOpen(true)}
            >
              <Trash2 />
              Delete Compound
            </Button>
          </CardContent>
        </Card>

        <ConfirmDialog
          open={deleteCompoundOpen}
          onOpenChange={setDeleteCompoundOpen}
          title="Delete Compound"
          description="Are you sure you want to delete"
          itemName={detail?.name}
          confirmText="Delete Compound"
          loading={deleteMutation.isPending}
          onConfirm={() => {
            deleteMutation.mutate(selectedId, {
              onSuccess: () => {
                setDeleteCompoundOpen(false);
                navigate('/compounds');
              },
            });
          }}
        />
        <ConfirmDialog
          open={!!removeMemberTarget}
          onOpenChange={(open) => !open && setRemoveMemberTarget(null)}
          title="Remove Server"
          description="Remove"
          itemName={removeMemberTarget?.name}
          confirmText="Remove"
          loading={removeMemberMutation.isPending}
          onConfirm={() => {
            if (removeMemberTarget) {
              removeMemberMutation.mutate(
                { compoundId: selectedId, serverId: removeMemberTarget.id },
                { onSuccess: () => setRemoveMemberTarget(null) },
              );
            }
          }}
        />
      </div>
    );
  }

  // --- List view ---
  return (
    <div className="space-y-5">
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

      {/* Info banner */}
      <InfoBanner
        icon={Layers}
        title="What are Compound Servers?"
        description="Compound servers group multiple MCP servers into a single endpoint. Instead of connecting to each server individually, your AI agent connects to one URL and gets access to all tools from the member servers."
        tips={[
          { label: 'Members', explanation: 'The MCP servers (including built-in memory/skills/tasks) included in this compound' },
          { label: 'Dictionary Mode', explanation: 'When on, the compound exposes a single "dictionary" tool instead of listing all tools upfront — agents discover and call tools lazily' },
          { label: 'Disabled Tools', explanation: 'Hide specific tools from clients accessing this compound without removing the member server' },
          { label: 'API Keys', explanation: 'Create API keys scoped to a compound to give clients access to only that group of servers' },
        ]}
      />

      {/* Compound list */}
      {compounds?.length === 0 && !showCreate ? (
        <Card>
          <CardContent className="py-12 text-center">
            <Layers className="size-10 text-muted-foreground/50 mx-auto mb-3" />
            <p className="text-sm text-muted-foreground">No compound servers yet</p>
            <p className="text-xs text-muted-foreground/70 mt-1">Create one to group multiple MCP servers into a single endpoint</p>
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
                      {c.description ? (
                        <div className="text-xs text-muted-foreground truncate">{c.description}</div>
                      ) : (
                        <div className="text-xs text-muted-foreground/60 truncate">No description</div>
                      )}
                    </div>
                  </div>
                  <div className="flex items-center gap-2 text-xs text-muted-foreground shrink-0">
                    {c.dictionary_mode && (
                      <Badge variant="secondary" className="text-[10px]">dictionary</Badge>
                    )}
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
