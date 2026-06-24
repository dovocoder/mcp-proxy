import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import {
  Plus,
  Trash2,
  RefreshCw,
  Server as ServerIcon,
  ChevronRight,
  Pencil,
  Search,
  Package,
  Cloud,
  Terminal,
  ArrowLeft,
  ExternalLink,
  ScrollText,
  Zap,
  Settings2,
  Shield,
  KeyRound,
} from 'lucide-react';
import { servers as serversApi, type Server, registry as registryApi, type RegistryServer } from '../api/client';
import { cn } from '../lib/utils';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardAction,
  CardContent,
} from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { Separator } from '@/components/ui/separator';
import { Switch } from '@/components/ui/switch';
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
import { ConfirmDialog } from '@/components/ConfirmDialog';

type Transport = 'stdio' | 'streamable-http' | 'http';

function statusBadge(status: string) {
  if (status === 'connected') return <Badge variant="default">{status}</Badge>;
  if (status === 'error') return <Badge variant="destructive">{status}</Badge>;
  return <Badge variant="secondary">{status}</Badge>;
}

export default function Servers() {
  const queryClient = useQueryClient();
  const { data: srvList } = useQuery({ queryKey: ['servers'], queryFn: serversApi.list });
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editServer, setEditServer] = useState<Server | null>(null);
  const [dialogView, setDialogView] = useState<'form' | 'registry'>('form');
  const [deleteTarget, setDeleteTarget] = useState<Server | null>(null);

  const deleteMutation = useMutation({
    mutationFn: serversApi.delete,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['servers'] });
      setDeleteTarget(null);
    },
  });

  const reconnectMutation = useMutation({
    mutationFn: serversApi.reconnect,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['servers'] }),
  });

  const handleOpenChange = (open: boolean) => {
    setDialogOpen(open);
    if (!open) {
      setEditServer(null);
      setDialogView('form');
    }
  };

  const handleEditOpen = (srv: Server) => {
    setEditServer(srv);
    setDialogView('form');
    setDialogOpen(true);
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-xl sm:text-2xl font-bold text-foreground truncate">Servers</h1>
          <p className="text-muted-foreground mt-1 text-sm">Manage backend MCP server connections</p>
        </div>
        <Dialog open={dialogOpen} onOpenChange={handleOpenChange}>
          <DialogTrigger
            render={
              <Button size="sm" className="shrink-0">
                <Plus className="size-4" />
                <span className="hidden sm:inline">Add Server</span>
                <span className="sm:hidden">Add</span>
              </Button>
            }
          />
          <DialogContent className="sm:max-w-xl max-h-[90vh] overflow-y-auto">
            {editServer ? (
              <ServerForm
                server={editServer}
                onClose={() => handleOpenChange(false)}
              />
            ) : (
              <>
                <DialogHeader>
                  <div className="flex items-center justify-between">
                    <DialogTitle>Add MCP Server</DialogTitle>
                    {dialogView === 'registry' && (
                      <Button
                        variant="ghost"
                        size="xs"
                        onClick={() => setDialogView('form')}
                      >
                        <ArrowLeft className="size-3.5" />
                        Back
                      </Button>
                    )}
                  </div>
                  <DialogDescription>
                    {dialogView === 'form'
                      ? 'Configure a server manually, or search the MCP registry.'
                      : 'Search the MCP server registry and import a server.'}
                  </DialogDescription>
                </DialogHeader>

                {dialogView === 'form' ? (
                  <>
                    <ServerForm
                      onClose={() => handleOpenChange(false)}
                      onRegistryView={() => setDialogView('registry')}
                    />
                  </>
                ) : (
                  <RegistrySearch
                    onPick={(srv) => {
                      setDialogView('form');
                      // Pass the registry server to the form via a custom event
                      // The form will read it and pre-fill fields
                      setEditServer(null);
                      // Store the picked server temporarily
                      window.dispatchEvent(new CustomEvent('registry-pick', { detail: srv }));
                    }}
                  />
                )}
              </>
            )}
          </DialogContent>
        </Dialog>
      </div>

      <div className="space-y-3">
        {srvList?.length === 0 && (
          <Card className="items-center py-10 text-center sm:py-14">
            <CardContent className="flex flex-col items-center">
              <ServerIcon className="size-10 text-muted-foreground/50 mb-3" />
              <p className="text-muted-foreground">No servers configured yet</p>
              <Button
                variant="link"
                size="sm"
                className="mt-2"
                onClick={() => setDialogOpen(true)}
              >
                Add your first server →
              </Button>
            </CardContent>
          </Card>
        )}

        {srvList?.map((srv) => (
          <Card key={srv.id} size="sm">
            <CardHeader>
              <Link to={`/servers/${srv.id}`} className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span
                    className={cn(
                      'size-2.5 shrink-0 rounded-full',
                      srv.status === 'connected'
                        ? 'bg-emerald-400'
                        : srv.status === 'error'
                          ? 'bg-red-400'
                          : 'bg-muted-foreground/40'
                    )}
                  />
                  <CardTitle className="truncate hover:text-primary transition-colors">
                    {srv.name}
                  </CardTitle>
                </div>
                <CardDescription className="truncate font-mono text-xs">
                  {srv.transport === 'stdio'
                    ? `${srv.command} ${(srv.args || []).join(' ')}`
                    : srv.url}
                </CardDescription>
              </Link>
              <CardAction className="flex items-center gap-1.5">
                {srv.is_builtin ? (
                  <Badge variant="secondary">builtin</Badge>
                ) : null}
                {statusBadge(srv.status)}
                {!srv.is_builtin && (
                  <>
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      onClick={() => reconnectMutation.mutate(srv.id)}
                      title="Reconnect"
                    >
                      <RefreshCw className="size-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      onClick={() => handleEditOpen(srv)}
                      title="Edit"
                    >
                      <Pencil className="size-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      onClick={() => setDeleteTarget(srv)}
                      title="Delete"
                      className="text-destructive hover:text-destructive"
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </>
                )}
              </CardAction>
            </CardHeader>
            <CardContent className="flex items-center gap-3 text-xs text-muted-foreground">
              <span className="uppercase tracking-wide">{srv.transport}</span>
              <Separator orientation="vertical" className="h-3" />
              <span>{srv.tools_count ?? 0} tools</span>
              {srv.logs_enabled && (
                <>
                  <Separator orientation="vertical" className="h-3" />
                  <span className="inline-flex items-center gap-0.5" title="Log capture enabled">
                    <ScrollText className="size-3" />
                    logs
                  </span>
                </>
              )}
              <Link
                to={`/servers/${srv.id}`}
                className="ml-auto inline-flex items-center gap-0.5 text-primary hover:underline"
              >
                Details <ChevronRight className="size-3" />
              </Link>
            </CardContent>
            {srv.live_error && (
              <CardContent className="pt-0">
                <div className="rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive break-words">
                  {srv.live_error}
                </div>
              </CardContent>
            )}
          </Card>
        ))}
      </div>

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Delete Server"
        description="Are you sure you want to delete"
        itemName={deleteTarget?.name}
        loading={deleteMutation.isPending}
        onConfirm={() => {
          if (deleteTarget) deleteMutation.mutate(deleteTarget.id);
        }}
      />
    </div>
  );
}

// --- Registry Search Component ---

function RegistrySearch({ onPick }: { onPick: (srv: RegistryServer) => void }) {
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<RegistryServer[]>([]);
  const [loading, setLoading] = useState(false);
  const [searched, setSearched] = useState(false);

  const handleSearch = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setSearched(true);
    try {
      const data = await registryApi.search(query || undefined);
      // The registry returns { servers: [...] } or an array
      const servers = Array.isArray(data) ? data : (data.servers || []);
      setResults(servers);
    } catch {
      setResults([]);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="space-y-4">
      <form onSubmit={handleSearch} className="flex gap-2">
        <div className="relative flex-1">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 size-4 text-muted-foreground" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search MCP registry..."
            className="pl-9"
            autoFocus
          />
        </div>
        <Button type="submit" disabled={loading} size="sm">
          {loading ? 'Searching...' : 'Search'}
        </Button>
      </form>

      <div className="max-h-[50vh] overflow-y-auto space-y-2 rounded-lg">
        {loading && (
          <div className="py-8 text-center text-sm text-muted-foreground">
            Searching registry...
          </div>
        )}

        {!loading && searched && results.length === 0 && (
          <div className="py-8 text-center text-sm text-muted-foreground">
            No servers found. Try a different search term.
          </div>
        )}

        {!loading && !searched && (
          <div className="py-8 text-center text-sm text-muted-foreground">
            <Package className="size-6 mx-auto mb-2 opacity-50" />
            Search the MCP registry to find servers to add
          </div>
        )}

        {results.map((srv, i) => {
          const pkg = srv.packages?.[0];
          const isStdio = pkg?.transport_type === 'stdio' || !!pkg?.command;
          const repoUrl = srv.repository?.url;
          return (
            <div
              key={srv.id || srv.name + i}
              className="rounded-lg border border-border bg-card p-3 hover:bg-accent/30 transition-colors"
            >
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    {isStdio ? (
                      <Terminal className="size-3.5 text-muted-foreground shrink-0" />
                    ) : (
                      <Cloud className="size-3.5 text-muted-foreground shrink-0" />
                    )}
                    <span className="font-medium text-foreground text-sm truncate">
                      {srv.name}
                    </span>
                    {pkg?.transport_type && (
                      <Badge variant="outline" className="text-[10px] shrink-0">
                        {pkg.transport_type}
                      </Badge>
                    )}
                  </div>
                  {srv.description && (
                    <p className="text-xs text-muted-foreground mt-1 line-clamp-2">
                      {srv.description}
                    </p>
                  )}
                  {repoUrl && (
                    <a
                      href={repoUrl}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex items-center gap-0.5 text-[11px] text-primary hover:underline mt-1"
                    >
                      <ExternalLink className="size-2.5" />
                      {repoUrl.replace('https://', '')}
                    </a>
                  )}
                </div>
                <Button
                  variant="outline"
                  size="xs"
                  className="shrink-0"
                  onClick={() => onPick(srv)}
                >
                  <Plus className="size-3" />
                  Use
                </Button>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

// --- Server Form Component ---

const TRANSPORT_OPTIONS = [
  {
    value: 'stdio' as Transport,
    label: 'Local Process',
    icon: Terminal,
    desc: 'Run a local stdio MCP server',
  },
  {
    value: 'streamable-http' as Transport,
    label: 'Remote HTTP',
    icon: Cloud,
    desc: 'Connect to a remote MCP server',
  },
  {
    value: 'http' as Transport,
    label: 'Legacy SSE',
    icon: Zap,
    desc: 'Older HTTP+SSE transport',
  },
];

function ServerForm({
  server,
  onClose,
  onRegistryView,
}: {
  server?: Server | null;
  onClose: () => void;
  onRegistryView?: () => void;
}) {
  const queryClient = useQueryClient();
  const isEdit = !!server;

  const [name, setName] = useState(server?.name || '');
  const [transport, setTransport] = useState<Transport>(
    (server?.transport as Transport) || 'stdio',
  );
  const [command, setCommand] = useState(server?.command || '');
  const [args, setArgs] = useState(server?.args?.join(' ') || '');
  const [url, setUrl] = useState(server?.url || '');
  const [headers, setHeaders] = useState(
    server?.headers
      ? Object.entries(server.headers).map(([k, v]) => `${k}: ${v}`).join('\n')
      : '',
  );
  const [env, setEnv] = useState(
    server?.env
      ? Object.entries(server.env).map(([k, v]) => `${k}=${v}`).join('\n')
      : '',
  );
  const [authToken, setAuthToken] = useState(server?.auth_token || '');
  const [timeout, setTimeout] = useState(server?.timeout ?? 120);
  const [connectTimeout, setConnectTimeout] = useState(server?.connect_timeout ?? 60);
  const [enabled, setEnabled] = useState(server?.enabled ?? true);
  const [logsEnabled, setLogsEnabled] = useState(server?.logs_enabled ?? true);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  // Listen for registry pick events
  useState(() => {
    const handler = (e: Event) => {
      const srv = (e as CustomEvent).detail as RegistryServer;
      const pkg = srv.packages?.[0];
      if (pkg) {
        setName(srv.name);
        if (pkg.transport_type === 'stdio' || pkg.command) {
          setTransport('stdio');
          setCommand(pkg.command || 'npx');
          setArgs((pkg.args || []).join(' '));
        } else if (pkg.url) {
          setTransport('streamable-http');
          setUrl(pkg.url);
        }
        if (pkg.env) {
          setEnv(Object.entries(pkg.env).map(([k, v]) => `${k}=${v}`).join('\n'));
        }
      }
    };
    window.addEventListener('registry-pick', handler);
    return () => window.removeEventListener('registry-pick', handler);
  });

  const isHTTP = transport === 'http' || transport === 'streamable-http';

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError('');

    try {
      const data: Partial<Server> = {
        name,
        transport,
        enabled,
        logs_enabled: logsEnabled,
        timeout,
        connect_timeout: connectTimeout,
      };

      if (transport === 'stdio') {
        data.command = command;
        data.args = args.split(/\s+/).filter(Boolean);
      } else {
        data.url = url;
        const hdrs: Record<string, string> = {};
        if (headers) {
          headers.split('\n').forEach((line) => {
            const idx = line.indexOf(':');
            if (idx > 0) hdrs[line.slice(0, idx).trim()] = line.slice(idx + 1).trim();
          });
        }
        data.headers = hdrs;
      }

      if (authToken) data.auth_token = authToken;

      const envVars: Record<string, string> = {};
      if (env) {
        env.split('\n').forEach((line) => {
          const idx = line.indexOf('=');
          if (idx > 0) envVars[line.slice(0, idx).trim()] = line.slice(idx + 1).trim();
        });
      }
      data.env = envVars;

      if (isEdit && server) {
        await serversApi.update(server.id, data);
      } else {
        await serversApi.create(data);
      }
      queryClient.invalidateQueries({ queryKey: ['servers'] });
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : `Failed to ${isEdit ? 'update' : 'create'} server`);
    } finally {
      setLoading(false);
    }
  };

  return (
    <form onSubmit={handleSubmit} className="flex flex-col gap-5">
      {!isEdit && (
        <DialogDescription>
          Configure a server manually, or search the MCP registry.
        </DialogDescription>
      )}
      {isEdit && (
        <DialogDescription>
          Update configuration for <span className="font-medium text-foreground">{server!.name}</span>
        </DialogDescription>
      )}

      {/* Transport Type — visual card picker */}
      <div className="grid gap-2">
        <Label className="flex items-center gap-1.5">
          <Settings2 className="size-3.5 text-muted-foreground" />
          Transport Type
        </Label>
        <div className="grid grid-cols-3 gap-2">
          {TRANSPORT_OPTIONS.map((opt) => {
            const isSelected = transport === opt.value;
            const Icon = opt.icon;
            return (
              <button
                key={opt.value}
                type="button"
                onClick={() => setTransport(opt.value)}
                className={cn(
                  'flex flex-col items-center gap-1.5 rounded-lg border p-3 text-center transition-all',
                  isSelected
                    ? 'border-primary bg-primary/10 text-primary'
                    : 'border-border bg-card text-muted-foreground hover:bg-accent/50',
                )}
              >
                <Icon className={cn('size-5', isSelected && 'text-primary')} />
                <span className={cn('text-xs font-medium', isSelected && 'text-primary')}>
                  {opt.label}
                </span>
                <span className="text-[10px] leading-tight text-muted-foreground line-clamp-2">
                  {opt.desc}
                </span>
              </button>
            );
          })}
        </div>
      </div>

      {/* Server Name */}
      <div className="grid gap-1.5">
        <Label htmlFor="srv-name">Server Name</Label>
        <Input
          id="srv-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="my-server"
          required
          className="font-mono"
        />
      </div>

      <Separator />

      {/* Connection details — varies by transport */}
      {transport === 'stdio' ? (
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div className="grid gap-1.5">
            <Label htmlFor="srv-command" className="flex items-center gap-1.5">
              <Terminal className="size-3.5 text-muted-foreground" />
              Command
            </Label>
            <Input
              id="srv-command"
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              className="font-mono"
              placeholder="npx"
              required
            />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="srv-args">Arguments</Label>
            <Input
              id="srv-args"
              value={args}
              onChange={(e) => setArgs(e.target.value)}
              className="font-mono"
              placeholder="-y @modelcontextprotocol/server-github"
            />
          </div>
          {/* Live preview */}
          {command && (
            <div className="sm:col-span-2 rounded-lg border border-border bg-muted/40 px-3 py-2">
              <span className="text-[10px] uppercase tracking-wide text-muted-foreground">Preview</span>
              <code className="block mt-1 text-xs font-mono text-foreground break-all">
                {command}
                {args && ` ${args}`}
              </code>
            </div>
          )}
        </div>
      ) : (
        <div className="grid gap-1.5">
          <Label htmlFor="srv-url" className="flex items-center gap-1.5">
            <Cloud className="size-3.5 text-muted-foreground" />
            Server URL
          </Label>
          <Input
            id="srv-url"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            className="font-mono"
            placeholder="https://mcp.dev.azure.com/my-org"
            required
          />
        </div>
      )}

      {/* Authentication (HTTP only) */}
      {isHTTP && (
        <div className="grid gap-3">
          <div className="grid gap-1.5">
            <Label htmlFor="srv-token" className="flex items-center gap-1.5">
              <KeyRound className="size-3.5 text-muted-foreground" />
              OAuth Client ID
              <span className="text-[10px] text-muted-foreground font-normal">(optional)</span>
            </Label>
            <Input
              id="srv-token"
              type="password"
              value={authToken}
              onChange={(e) => setAuthToken(e.target.value)}
              className="font-mono"
              placeholder="Leave empty for auto-discovery"
            />
            <p className="text-xs text-muted-foreground">
              Pre-registered OAuth client_id. Leave empty to use dynamic registration,
              CIMD, or Entra ID public client. For manual bearer tokens or env var tokens,
              configure them from the server detail page after creation.
            </p>
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="srv-headers" className="flex items-center gap-1.5">
              <Shield className="size-3.5 text-muted-foreground" />
              Additional Headers
              <span className="text-[10px] text-muted-foreground font-normal">(one per line)</span>
            </Label>
            <Textarea
              id="srv-headers"
              value={headers}
              onChange={(e) => setHeaders(e.target.value)}
              rows={2}
              className="font-mono text-xs"
              placeholder="X-Custom-Header: value"
            />
          </div>
        </div>
      )}

      {/* Environment Variables */}
      <div className="grid gap-1.5">
        <Label htmlFor="srv-env" className="flex items-center gap-1.5">
          <Settings2 className="size-3.5 text-muted-foreground" />
          Environment Variables
          <span className="text-[10px] text-muted-foreground font-normal">(one per line, KEY=value)</span>
        </Label>
        <Textarea
          id="srv-env"
          value={env}
          onChange={(e) => setEnv(e.target.value)}
          rows={3}
          className="font-mono text-xs"
          placeholder="GITHUB_PERSONAL_ACCESS_TOKEN=ghp_..."
        />
      </div>

      <Separator />

      {/* Advanced Settings */}
      <div className="grid gap-4">
        <span className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground uppercase tracking-wide">
          <Settings2 className="size-3" />
          Advanced
        </span>
        <div className="grid grid-cols-2 gap-4">
          <div className="grid gap-1.5">
            <Label htmlFor="srv-timeout">Request Timeout (s)</Label>
            <Input
              id="srv-timeout"
              type="number"
              value={timeout}
              onChange={(e) => setTimeout(Number(e.target.value) || 120)}
              min={1}
              className="font-mono"
            />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="srv-conn-timeout">Connect Timeout (s)</Label>
            <Input
              id="srv-conn-timeout"
              type="number"
              value={connectTimeout}
              onChange={(e) => setConnectTimeout(Number(e.target.value) || 60)}
              min={1}
              className="font-mono"
            />
          </div>
        </div>

        {/* Toggle switches */}
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <div className="flex items-center justify-between rounded-lg border border-border px-3 py-2.5">
            <div className="min-w-0 pr-3">
              <Label className="text-sm">Enabled</Label>
              <p className="text-xs text-muted-foreground">Connect on startup</p>
            </div>
            <Switch
              checked={enabled}
              onCheckedChange={setEnabled}
              className="shrink-0"
            />
          </div>
          <div className="flex items-center justify-between rounded-lg border border-border px-3 py-2.5">
            <div className="min-w-0 pr-3">
              <Label className="text-sm flex items-center gap-1">
                <ScrollText className="size-3.5" />
                Log Capture
              </Label>
              <p className="text-xs text-muted-foreground">Capture stderr output</p>
            </div>
            <Switch
              checked={logsEnabled}
              onCheckedChange={setLogsEnabled}
              className="shrink-0"
            />
          </div>
        </div>
      </div>

      {error && (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive break-words">
          {error}
        </div>
      )}

      {!isEdit && onRegistryView && (
        <button
          type="button"
          onClick={onRegistryView}
          className="inline-flex items-center gap-1.5 text-xs text-primary hover:underline self-start"
        >
          <Search className="size-3" />
          Search MCP registry instead
        </button>
      )}

      <DialogFooter>
        <DialogClose render={<Button variant="outline" type="button">Cancel</Button>} />
        <Button type="submit" disabled={loading}>
          {loading
            ? isEdit ? 'Saving...' : 'Creating...'
            : isEdit ? 'Save Changes' : 'Create Server'}
        </Button>
      </DialogFooter>
    </form>
  );
}
