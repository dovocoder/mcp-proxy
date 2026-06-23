import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import {
  Plus,
  Trash2,
  RefreshCw,
  Server as ServerIcon,
  Cloud,
  Zap,
  Terminal,
  ChevronRight,
  Pencil,
} from 'lucide-react';
import { servers as serversApi, type Server } from '../api/client';
import { cn } from '../lib/utils';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardAction,
  CardContent,
  CardFooter,
} from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
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

type Transport = 'stdio' | 'streamable-http' | 'http';

interface PresetConfig {
  name: string;
  transport: Transport;
  command?: string;
  args?: string;
  url?: string;
  urlHint?: string;
}

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
    } as PresetConfig,
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
    } as PresetConfig,
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
    } as PresetConfig,
  },
  {
    id: 'custom',
    name: 'Custom',
    icon: Plus,
    description: 'Configure manually',
    config: null,
  },
] as const;

function statusBadge(status: string) {
  if (status === 'connected') return <Badge variant="default">{status}</Badge>;
  if (status === 'error') return <Badge variant="destructive">{status}</Badge>;
  return <Badge variant="secondary">{status}</Badge>;
}

export default function Servers() {
  const queryClient = useQueryClient();
  const { data: srvList } = useQuery({ queryKey: ['servers'], queryFn: serversApi.list });
  const [dialogOpen, setDialogOpen] = useState(false);
  const [preset, setPreset] = useState<string | null>(null);
  const [editServer, setEditServer] = useState<Server | null>(null);

  const deleteMutation = useMutation({
    mutationFn: serversApi.delete,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['servers'] }),
  });

  const reconnectMutation = useMutation({
    mutationFn: serversApi.reconnect,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['servers'] }),
  });

  const handleOpenChange = (open: boolean) => {
    setDialogOpen(open);
    if (!open) {
      setPreset(null);
      setEditServer(null);
    }
  };

  const handleEditOpen = (srv: Server) => {
    setEditServer(srv);
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
          <DialogContent className="sm:max-w-lg">
            {editServer ? (
              <ServerForm
                server={editServer}
                onClose={() => {
                  setDialogOpen(false);
                  setEditServer(null);
                }}
              />
            ) : !preset ? (
              <>
                <DialogHeader>
                  <DialogTitle>Choose a preset</DialogTitle>
                  <DialogDescription>
                    Pick a template to pre-fill the server form, or start from scratch.
                  </DialogDescription>
                </DialogHeader>
                <div className="grid grid-cols-2 gap-3">
                  {PRESETS.map((p) => {
                    const Icon = p.icon;
                    return (
                      <button
                        key={p.id}
                        onClick={() => setPreset(p.id)}
                        className="flex flex-col items-center gap-2 rounded-lg border border-border bg-background p-4 text-center transition-colors hover:bg-muted hover:border-primary/50"
                      >
                        <Icon className="size-6 text-primary" />
                        <div className="font-medium text-foreground text-sm">{p.name}</div>
                        <div className="text-xs text-muted-foreground">{p.description}</div>
                      </button>
                    );
                  })}
                </div>
                <DialogFooter showCloseButton />
              </>
            ) : (
              <ServerForm
                preset={PRESETS.find((p) => p.id === preset)!}
                onClose={() => {
                  setDialogOpen(false);
                  setPreset(null);
                }}
              />
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
                {statusBadge(srv.status)}
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
                  onClick={() => deleteMutation.mutate(srv.id)}
                  title="Delete"
                  className="text-destructive hover:text-destructive"
                >
                  <Trash2 className="size-4" />
                </Button>
              </CardAction>
            </CardHeader>
            <CardContent className="flex items-center gap-3 text-xs text-muted-foreground">
              <span className="uppercase tracking-wide">{srv.transport}</span>
              <Separator orientation="vertical" className="h-3" />
              <span>{srv.tools_count ?? 0} tools</span>
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
    </div>
  );
}

function ServerForm({
  preset,
  server,
  onClose,
}: {
  preset?: { id: string; name: string; config: PresetConfig | null };
  server?: Server | null;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const isEdit = !!server;
  const cfg = preset?.config;

  const [name, setName] = useState(server?.name || cfg?.name || '');
  const [transport, setTransport] = useState<Transport>(
    (server?.transport as Transport) || cfg?.transport || 'stdio',
  );
  const [command, setCommand] = useState(server?.command || cfg?.command || '');
  const [args, setArgs] = useState(
    server?.args?.join(' ') || cfg?.args || '',
  );
  const [url, setUrl] = useState(server?.url || cfg?.url || '');
  const [headers, setHeaders] = useState(
    server?.headers
      ? Object.entries(server.headers)
          .map(([k, v]) => `${k}: ${v}`)
          .join('\n')
      : '',
  );
  const [env, setEnv] = useState(
    server?.env
      ? Object.entries(server.env)
          .map(([k, v]) => `${k}=${v}`)
          .join('\n')
      : '',
  );
  const [authToken, setAuthToken] = useState(server?.auth_token || '');
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
        enabled: server?.enabled ?? true,
        timeout: server?.timeout ?? 120,
        connect_timeout: server?.connect_timeout ?? 60,
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
        } else {
          data.headers = {};
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
      } else {
        data.env = {};
      }

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
    <form onSubmit={handleSubmit} className="flex flex-col gap-4">
      <DialogHeader>
        <DialogTitle>{isEdit ? 'Edit MCP Server' : 'Add MCP Server'}</DialogTitle>
        <DialogDescription>
          {isEdit ? (
            <>Update configuration for <span className="font-medium text-foreground">{server!.name}</span></>
          ) : (
            <>Preset: <span className="font-medium text-foreground">{preset?.name}</span></>
          )}
        </DialogDescription>
      </DialogHeader>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <div className="grid gap-1.5">
          <Label htmlFor="srv-name">Name</Label>
          <Input
            id="srv-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="my-server"
            required
          />
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor="srv-transport">Transport</Label>
          <Select value={transport} onValueChange={(v) => setTransport(v as Transport)}>
            <SelectTrigger id="srv-transport" className="w-full">
              <SelectValue placeholder="Select transport" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="stdio">stdio (local process)</SelectItem>
              <SelectItem value="streamable-http">streamable-http (remote)</SelectItem>
              <SelectItem value="http">http (legacy SSE)</SelectItem>
            </SelectContent>
          </Select>
        </div>
      </div>

      {transport === 'stdio' ? (
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div className="grid gap-1.5">
            <Label htmlFor="srv-command">Command</Label>
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
            <Label htmlFor="srv-args">Args (space-separated)</Label>
            <Input
              id="srv-args"
              value={args}
              onChange={(e) => setArgs(e.target.value)}
              className="font-mono"
              placeholder="-y @modelcontextprotocol/server-github"
            />
          </div>
        </div>
      ) : (
        <div className="grid gap-1.5">
          <Label htmlFor="srv-url">
            URL
            {cfg?.urlHint && (
              <span className="text-muted-foreground font-normal"> ({cfg.urlHint})</span>
            )}
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

      {isHTTP && (
        <div className="grid gap-1.5">
          <Label htmlFor="srv-token">Auth Token</Label>
          <Input
            id="srv-token"
            type="password"
            value={authToken}
            onChange={(e) => setAuthToken(e.target.value)}
            className="font-mono"
            placeholder="Leave empty for Entra ID (auto-detected)"
          />
          <p className="text-xs text-muted-foreground">
            Optional — used as client_id for OAuth. For Entra ID / Azure DevOps: leave empty and
            OAuth will use a built-in public client. Enter a client_id only if you have a custom app
            registration.
          </p>
        </div>
      )}

      {isHTTP && (
        <div className="grid gap-1.5">
          <Label htmlFor="srv-headers">Additional Headers (one per line, key: value)</Label>
          <Textarea
            id="srv-headers"
            value={headers}
            onChange={(e) => setHeaders(e.target.value)}
            rows={3}
            className="font-mono"
            placeholder="X-Custom-Header: value"
          />
        </div>
      )}

      <div className="grid gap-1.5">
        <Label htmlFor="srv-env">Environment Variables (one per line, KEY=value)</Label>
        <Textarea
          id="srv-env"
          value={env}
          onChange={(e) => setEnv(e.target.value)}
          rows={3}
          className="font-mono"
          placeholder="GITHUB_PERSONAL_ACCESS_TOKEN=ghp_..."
        />
      </div>

      {error && (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive break-words">
          {error}
        </div>
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
