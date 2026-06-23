import { useState } from 'react';
import { useParams, Link } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  ArrowLeft,
  RefreshCw,
  Trash2,
  Wrench,
  LogIn,
  ShieldCheck,
  ShieldAlert,
  ExternalLink,
  Link as LinkIcon,
  Copy,
  Check,
  Loader2,
  Terminal,
  Eraser,
} from 'lucide-react';
import { servers as serversApi, tools as toolsApi, type DeviceAuthResult } from '../api/client';
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
import { Separator } from '@/components/ui/separator';

function statusBadge(status: string) {
  if (status === 'connected') return <Badge variant="default">{status}</Badge>;
  if (status === 'error') return <Badge variant="destructive">{status}</Badge>;
  return <Badge variant="secondary">{status}</Badge>;
}

function ConfigRow({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <div className="flex items-start justify-between gap-4">
      <dt className="text-sm text-muted-foreground shrink-0">{label}</dt>
      <dd
        className={cn(
          'text-sm text-foreground text-right break-all min-w-0',
          mono && 'font-mono'
        )}
      >
        {value}
      </dd>
    </div>
  );
}

export default function ServerDetail() {
  const { id } = useParams<{ id: string }>();
  const queryClient = useQueryClient();
  const [copiedUrl, setCopiedUrl] = useState<string | null>(null);
  const [deviceAuth, setDeviceAuth] = useState<DeviceAuthResult | null>(null);
  const [devicePolling, setDevicePolling] = useState(false);
  const [deviceError, setDeviceError] = useState<string | null>(null);

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
    setCopiedUrl(text);
    setTimeout(() => setCopiedUrl(null), 2000);
  };

  const { data } = useQuery({
    queryKey: ['server', id],
    queryFn: () => serversApi.get(id!),
    enabled: !!id,
  });

  const { data: allTools } = useQuery({ queryKey: ['tools'], queryFn: toolsApi.list });

  const isHTTP =
    data?.server.transport === 'http' || data?.server.transport === 'streamable-http';

  const { data: authStatus } = useQuery({
    queryKey: ['auth-status', id],
    queryFn: () => serversApi.authStatus(id!),
    enabled: !!id && isHTTP,
    refetchInterval: 5000,
  });

  const isStdio = data?.server.transport === 'stdio';

  const { data: logsData } = useQuery({
    queryKey: ['server-logs', id],
    queryFn: () => serversApi.logs(id!),
    enabled: !!id && isStdio,
    refetchInterval: 3000,
  });

  const clearLogsMutation = useMutation({
    mutationFn: () => serversApi.clearLogs(id!),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['server-logs', id] }),
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

  const initiateDeviceAuth = async () => {
    if (!id) return;
    setDeviceError(null);
    try {
      const res = await serversApi.initiateDeviceAuth(id);
      setDeviceAuth(res);
      setDevicePolling(true);
      void pollDeviceAuth(id, res.interval || 5);
    } catch (err) {
      setDeviceError(err instanceof Error ? err.message : 'Failed to start device auth');
    }
  };

  const pollDeviceAuth = async (serverId: string, interval: number) => {
    try {
      const res = await serversApi.pollDeviceAuth(serverId);
      if (res.completed) {
        setDevicePolling(false);
        setDeviceAuth(null);
        queryClient.invalidateQueries({ queryKey: ['auth-status', serverId] });
        queryClient.invalidateQueries({ queryKey: ['server', serverId] });
        return;
      }
      if (res.expired) {
        setDevicePolling(false);
        setDeviceAuth(null);
        setDeviceError('Device authentication expired. Please try again.');
        return;
      }
      setTimeout(() => void pollDeviceAuth(serverId, interval), interval * 1000);
    } catch (err) {
      setDevicePolling(false);
      setDeviceAuth(null);
      setDeviceError(err instanceof Error ? err.message : 'Device auth polling failed');
    }
  };

  if (!data) return <div className="text-muted-foreground p-4">Loading...</div>;

  const srv = data.server;
  const serverTools = allTools?.filter((t) => t.server_id === id) || [];

  const mcpUrl = `/api/servers/${id}/mcp`;
  const sseUrl = `/api/servers/${id}/sse`;

  const renderCopyField = (label: string, method: string, url: string) => (
    <div className="grid gap-1.5">
      <Label className="flex items-center gap-2">
        <span
          className={cn(
            'rounded px-1.5 py-0.5 font-mono text-xs',
            method === 'POST'
              ? 'bg-primary/15 text-primary'
              : 'bg-emerald-500/15 text-emerald-400'
          )}
        >
          {method}
        </span>
        <span className="text-xs text-muted-foreground">{label}</span>
      </Label>
      <div className="flex items-center gap-2">
        <Input readOnly value={url} className="font-mono text-xs" />
        <Button
          variant="outline"
          size="icon"
          onClick={() => copyToClipboard(url)}
          aria-label="Copy to clipboard"
          className="shrink-0"
        >
          {copiedUrl === url ? (
            <Check className="size-4 text-emerald-400" />
          ) : (
            <Copy className="size-4" />
          )}
        </Button>
      </div>
    </div>
  );

  const authStatusLabel =
    authStatus?.status === 'valid'
      ? 'Authenticated'
      : authStatus?.status === 'expired'
        ? 'Token expired — re-authenticate'
        : 'Not authenticated';

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center gap-3 sm:gap-4">
        <Button variant="ghost" size="icon" render={<Link to="/servers" />} className="shrink-0">
          <ArrowLeft className="size-5" />
        </Button>
        <div className="flex-1 min-w-0">
          <h1 className="text-xl sm:text-2xl font-bold text-foreground truncate">{srv.name}</h1>
          <p className="text-sm text-muted-foreground mt-1">{srv.transport} transport</p>
        </div>
        {statusBadge(srv.status)}
      </div>

      {data.live_error && (
        <div className="rounded-xl border border-destructive/40 bg-destructive/10 px-4 py-3 text-sm text-destructive break-words">
          {data.live_error}
        </div>
      )}

      {/* OAuth Authentication */}
      {isHTTP && (
        <Card>
          <CardHeader>
            <div className="flex items-center gap-2">
              {authStatus?.status === 'valid' ? (
                <ShieldCheck className="size-5 text-emerald-400 shrink-0" />
              ) : (
                <ShieldAlert className="size-5 text-amber-400 shrink-0" />
              )}
              <div>
                <CardTitle>OAuth Authentication</CardTitle>
                <CardDescription>
                  Status:{' '}
                  <span
                    className={cn(
                      'font-medium',
                      authStatus?.status === 'valid'
                        ? 'text-emerald-400'
                        : authStatus?.status === 'expired'
                          ? 'text-amber-400'
                          : 'text-muted-foreground'
                    )}
                  >
                    {authStatusLabel}
                  </span>
                </CardDescription>
              </div>
            </div>
            <CardAction>
              <Button
                onClick={initiateDeviceAuth}
                disabled={devicePolling}
                size="sm"
              >
                {devicePolling ? (
                  <Loader2 className="size-4 animate-spin" />
                ) : (
                  <LogIn className="size-4" />
                )}
                {authStatus?.status === 'valid' ? 'Re-authenticate' : 'Sign in with Microsoft'}
              </Button>
            </CardAction>
          </CardHeader>
          <CardContent className="space-y-3">
            <p className="text-xs text-muted-foreground">
              No client_id needed — just sign in with your Microsoft account.
            </p>

            {deviceError && (
              <div className="text-xs text-destructive break-words">{deviceError}</div>
            )}

            {deviceAuth && (
              <div className="rounded-lg border border-border bg-muted/40 p-4 space-y-3">
                <p className="text-sm text-muted-foreground">
                  Visit the link below and enter this code:
                </p>
                <div className="text-center">
                  <div className="font-mono text-3xl sm:text-4xl font-bold tracking-[0.2em] text-foreground">
                    {deviceAuth.user_code}
                  </div>
                </div>
                <div className="text-center">
                  <a
                    href={deviceAuth.verification_uri}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-1.5 text-primary hover:underline break-all"
                  >
                    {deviceAuth.verification_uri}
                    <ExternalLink className="size-4 shrink-0" />
                  </a>
                </div>
                <div className="flex items-center justify-center gap-2 text-xs text-muted-foreground">
                  <Loader2 className="size-3.5 animate-spin" />
                  Polling for completion...
                </div>
              </div>
            )}
          </CardContent>
        </Card>
      )}

      {/* Connection URLs */}
      <Card>
        <CardHeader>
          <div className="flex items-center gap-2">
            <LinkIcon className="size-4 text-primary shrink-0" />
            <div>
              <CardTitle>Connection URLs</CardTitle>
              <CardDescription>
                Use these endpoints with an API key to connect MCP clients to this server.
              </CardDescription>
            </div>
          </div>
        </CardHeader>
        <CardContent className="space-y-3">
          {renderCopyField('MCP endpoint', 'POST', mcpUrl)}
          <Separator />
          {renderCopyField('SSE endpoint', 'SSE', sseUrl)}
        </CardContent>
      </Card>

      {/* Debug Logs (stdio only) */}
      {isStdio && (
        <Card>
          <CardHeader>
            <div className="flex items-center gap-2">
              <Terminal className="size-4 text-muted-foreground shrink-0" />
              <div>
                <CardTitle>Debug Logs</CardTitle>
                <CardDescription>
                  stderr output from the stdio subprocess. Auto-refreshes every 3s.
                  {logsData && logsData.count > 0 && ` ${logsData.count} lines captured.`}
                </CardDescription>
              </div>
            </div>
            <CardAction>
              <Button
                variant="outline"
                size="sm"
                onClick={() => clearLogsMutation.mutate()}
                disabled={clearLogsMutation.isPending || !logsData?.count}
              >
                <Eraser className="size-4" />
                <span className="hidden sm:inline">Clear</span>
              </Button>
            </CardAction>
          </CardHeader>
          <CardContent>
            <div className="rounded-lg bg-black/60 border border-border overflow-hidden">
              {logsData && logsData.logs && logsData.logs.length > 0 ? (
                <div className="max-h-80 overflow-y-auto p-3 font-mono text-xs space-y-0.5">
                  {logsData.logs.map((entry, i) => (
                    <div key={i} className="flex gap-3 hover:bg-white/5 px-1 py-0.5 rounded">
                      <span className="text-muted-foreground/60 shrink-0">
                        {new Date(entry.timestamp).toLocaleTimeString()}
                      </span>
                      <span className="text-green-400/90 break-all whitespace-pre-wrap min-w-0">
                        {entry.line}
                      </span>
                    </div>
                  ))}
                </div>
              ) : (
                <div className="flex items-center justify-center py-8 text-muted-foreground text-sm">
                  <Terminal className="size-5 mr-2 opacity-50" />
                  No stderr output yet
                </div>
              )}
            </div>
          </CardContent>
        </Card>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 sm:gap-6">
        {/* Configuration */}
        <Card>
          <CardHeader>
            <CardTitle>Configuration</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <dl className="space-y-3">
              <ConfigRow label="Transport" value={srv.transport} mono />
              {srv.transport === 'stdio' ? (
                <>
                  <ConfigRow label="Command" value={srv.command} mono />
                  {srv.args && srv.args.length > 0 && (
                    <div className="grid gap-1">
                      <dt className="text-sm text-muted-foreground">Args</dt>
                      <dd className="rounded-lg bg-muted px-3 py-2 text-sm text-foreground font-mono break-all">
                        {srv.args.join(' ')}
                      </dd>
                    </div>
                  )}
                </>
              ) : (
                <ConfigRow label="URL" value={srv.url} mono />
              )}
              <ConfigRow label="Timeout" value={`${srv.timeout}s`} />
              <ConfigRow label="Connect Timeout" value={`${srv.connect_timeout}s`} />
              <ConfigRow label="Enabled" value={srv.enabled ? 'Yes' : 'No'} />
              {srv.env && Object.keys(srv.env).length > 0 && (
                <div className="grid gap-1">
                  <dt className="text-sm text-muted-foreground">Environment</dt>
                  <dd className="rounded-lg bg-muted px-3 py-2 text-sm text-foreground font-mono whitespace-pre-line break-all">
                    {Object.keys(srv.env)
                      .map((k) => `${k}=***`)
                      .join('\n')}
                  </dd>
                </div>
              )}
              {srv.auth_token && <ConfigRow label="Auth Token" value="••••••••••••" mono />}
            </dl>
          </CardContent>
          <Separator />
          <CardContent className="flex flex-wrap gap-3 pt-4">
            <Button onClick={() => reconnectMutation.mutate()} disabled={reconnectMutation.isPending}>
              <RefreshCw className={cn('size-4', reconnectMutation.isPending && 'animate-spin')} />
              Reconnect
            </Button>
            <Button
              variant="destructive"
              onClick={() => deleteMutation.mutate()}
              disabled={deleteMutation.isPending}
            >
              <Trash2 className="size-4" />
              Delete
            </Button>
          </CardContent>
        </Card>

        {/* Tools */}
        <Card>
          <CardHeader>
            <div className="flex items-center gap-2">
              <Wrench className="size-4 text-muted-foreground shrink-0" />
              <CardTitle>Tools ({serverTools.length})</CardTitle>
            </div>
          </CardHeader>
          <CardContent>
            <div className="space-y-2 max-h-96 overflow-y-auto pr-1">
              {serverTools.length === 0 ? (
                <p className="text-sm text-muted-foreground text-center py-8">
                  No tools discovered
                </p>
              ) : (
                serverTools.map((tool) => (
                  <div
                    key={tool.name}
                    className="rounded-lg bg-muted px-4 py-3 break-words"
                  >
                    <div className="font-medium text-foreground text-sm truncate">{tool.name}</div>
                    {tool.description && (
                      <div className="text-xs text-muted-foreground mt-1 break-words">
                        {tool.description}
                      </div>
                    )}
                  </div>
                ))
              )}
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
