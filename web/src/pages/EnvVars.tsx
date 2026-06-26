import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  Plus,
  Trash2,
  Lock,
  Copy,
  Check,
  Eye,
  EyeOff,
  Terminal,
  KeyRound,
  Pencil,
  Link2,
  Info,
} from 'lucide-react';
import { envVars as envVarsApi, type EnvVar } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Separator } from '@/components/ui/separator';
import { Textarea } from '@/components/ui/textarea';
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

interface DialogState {
  open: boolean;
  mode: 'create' | 'edit';
  editId?: string;
  project: string;
  environment: string;
  key: string;
  value: string;
}

const emptyDialog: DialogState = {
  open: false,
  mode: 'create',
  project: '',
  environment: '',
  key: '',
  value: '',
};

export default function EnvVars() {
  const queryClient = useQueryClient();
  const [selectedProject, setSelectedProject] = useState<string>('');
  const [selectedEnv, setSelectedEnv] = useState<string>('');
  const [dialog, setDialog] = useState<DialogState>(emptyDialog);
  const [showValues, setShowValues] = useState<Record<string, boolean>>({});
  const [copiedUrl, setCopiedUrl] = useState<string | null>(null);
  const [formError, setFormError] = useState('');
  const [deleteTarget, setDeleteTarget] = useState<EnvVar | null>(null);

  const { data: projects } = useQuery({
    queryKey: ['env-var-projects'],
    queryFn: envVarsApi.projects,
  });

  const { data: environments } = useQuery({
    queryKey: ['env-var-envs', selectedProject],
    queryFn: () => envVarsApi.environments(selectedProject),
    enabled: !!selectedProject,
  });

  const { data: envVars } = useQuery({
    queryKey: ['env-vars', selectedProject, selectedEnv],
    queryFn: () => envVarsApi.list(selectedProject || undefined, selectedEnv || undefined),
  });

  const createMutation = useMutation({
    mutationFn: (data: { project: string; environment: string; key: string; value: string }) =>
      envVarsApi.create(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['env-vars'] });
      queryClient.invalidateQueries({ queryKey: ['env-var-projects'] });
      queryClient.invalidateQueries({ queryKey: ['env-var-envs'] });
      setDialog(emptyDialog);
      setFormError('');
    },
    onError: (err: Error) => setFormError(err.message),
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, value }: { id: string; value: string }) =>
      envVarsApi.update(id, { value }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['env-vars'] });
      setDialog(emptyDialog);
      setFormError('');
    },
    onError: (err: Error) => setFormError(err.message),
  });

  const deleteMutation = useMutation({
    mutationFn: envVarsApi.delete,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['env-vars'] }),
  });

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
    setCopiedUrl(text);
    setTimeout(() => setCopiedUrl(null), 2000);
  };

  const toggleValue = (id: string) => {
    setShowValues((prev) => ({ ...prev, [id]: !prev[id] }));
  };

  // Available keys in the current project/environment for reference hints
  const availableKeys = (envVars || [])
    .filter((ev) => !dialog.editId || ev.id !== dialog.editId)
    .map((ev) => ev.key);

  const insertReference = (key: string) => {
    setDialog((prev) => ({
      ...prev,
      value: prev.value + '${' + key + '}',
    }));
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    setFormError('');
    if (dialog.mode === 'create') {
      createMutation.mutate({
        project: dialog.project,
        environment: dialog.environment,
        key: dialog.key,
        value: dialog.value,
      });
    } else if (dialog.mode === 'edit' && dialog.editId) {
      updateMutation.mutate({ id: dialog.editId, value: dialog.value });
    }
  };

  const isPending = createMutation.isPending || updateMutation.isPending;

  const exportUrl = selectedProject
    ? `${window.location.origin}/api/env-vars/export?project=${encodeURIComponent(selectedProject)}${selectedEnv ? `&environment=${encodeURIComponent(selectedEnv)}` : ''}`
    : '';

  const decryptSnippet = `# Python — decrypt env vars locally with API key
import nacl.secret, base64, hashlib, json, requests

api_key = "mcp_your_api_key_here"
resp = requests.get(
    "${typeof window !== 'undefined' ? window.location.origin : 'https://your-proxy'}/api/env-vars/export",
    params={"project": "${selectedProject || 'my-project'}", "environment": "${selectedEnv || 'dev'}"},
    headers={"Authorization": f"Bearer {api_key}"}
)
data = resp.json()["encrypted"]
key = hashlib.sha256(api_key.encode()).digest()
box = nacl.secret.SecretBox(key)
raw = base64.b64decode(data)
env_vars = json.loads(box.decrypt(raw[24:], raw[:24]))
print(env_vars)  # {"DATABASE_URL": "...", "API_SECRET": "..."}`;

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-start sm:items-center justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-xl sm:text-2xl font-bold text-foreground">Env Variables</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Encrypted env vars per project & environment. Values can reference other keys with{' '}
            <code className="text-xs bg-muted px-1 py-0.5 rounded">{'${KEY}'}</code> or specific project/env vars with{' '}
            <code className="text-xs bg-muted px-1 py-0.5 rounded">{'$[project:env:var]'}</code>.
          </p>
        </div>
        <Dialog
          open={dialog.open}
          onOpenChange={(open) => {
            if (!open) {
              setDialog(emptyDialog);
              setFormError('');
            }
          }}
        >
          <DialogTrigger
            render={
              <Button
                className="shrink-0"
                onClick={() => {
                  setDialog({
                    ...emptyDialog,
                    open: true,
                    mode: 'create',
                    project: selectedProject,
                    environment: selectedEnv,
                  });
                }}
              >
                <Plus />
                <span className="hidden sm:inline">Add Variable</span>
                <span className="sm:hidden">Add</span>
              </Button>
            }
          />
          <DialogContent className="sm:max-w-lg max-h-[90vh] overflow-y-auto bg-background border-border">
            <DialogHeader>
              <DialogTitle>
                {dialog.mode === 'create' ? 'Add Environment Variable' : 'Edit Variable'}
              </DialogTitle>
              <DialogDescription>
                {dialog.mode === 'create'
                  ? 'Values are encrypted at rest with NaCl (Sodium) secretbox.'
                  : `Editing ${dialog.key} — values are encrypted at rest.`}
              </DialogDescription>
            </DialogHeader>

            <form onSubmit={handleSubmit} className="space-y-4">
              {/* Project + Environment */}
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-2">
                  <Label htmlFor="ev-project" className="text-xs text-muted-foreground">
                    Project
                  </Label>
                  <Input
                    id="ev-project"
                    value={dialog.project}
                    onChange={(e) => setDialog((prev) => ({ ...prev, project: e.target.value }))}
                    placeholder="my-app"
                    disabled={dialog.mode === 'edit'}
                    className="bg-muted/50 border-border"
                    required
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="ev-env" className="text-xs text-muted-foreground">
                    Environment
                  </Label>
                  <Input
                    id="ev-env"
                    value={dialog.environment}
                    onChange={(e) => setDialog((prev) => ({ ...prev, environment: e.target.value }))}
                    placeholder="dev"
                    disabled={dialog.mode === 'edit'}
                    className="bg-muted/50 border-border"
                    required
                  />
                </div>
              </div>

              {/* Key */}
              <div className="space-y-2">
                <Label htmlFor="ev-key" className="text-xs text-muted-foreground">
                  Key
                </Label>
                <Input
                  id="ev-key"
                  value={dialog.key}
                  onChange={(e) => setDialog((prev) => ({ ...prev, key: e.target.value }))}
                  placeholder="DATABASE_URL"
                  disabled={dialog.mode === 'edit'}
                  className="bg-muted/50 border-border font-mono"
                  required
                />
              </div>

              {/* Value */}
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <Label htmlFor="ev-value" className="text-xs text-muted-foreground">
                    Value
                  </Label>
                  {dialog.value.includes('${') && (
                    <Badge variant="secondary" className="text-[10px] gap-1">
                      <Link2 className="size-2.5" />
                      has reference
                    </Badge>
                  )}
                </div>
                <Textarea
                  id="ev-value"
                  value={dialog.value}
                  onChange={(e) => setDialog((prev) => ({ ...prev, value: e.target.value }))}
                  placeholder="postgresql://... or ${DATABASE_URL}"
                  className="bg-muted/50 border-border font-mono text-sm min-h-[80px] resize-y"
                  required
                />
                <p className="text-[11px] text-muted-foreground/70">
                  Use <code className="text-foreground bg-muted px-1 py-0.5 rounded text-[10px]">{'${KEY}'}</code> to
                  reference another variable, or <code className="text-foreground bg-muted px-1 py-0.5 rounded text-[10px]">{'${KEY:-default}'}</code> for a fallback.
                </p>
              </div>

              {/* Reference hints */}
              {availableKeys.length > 0 && (
                <div className="space-y-2">
                  <div className="flex items-center gap-1.5">
                    <Info className="size-3 text-muted-foreground" />
                    <span className="text-xs text-muted-foreground">Available keys to reference</span>
                  </div>
                  <div className="flex flex-wrap gap-1.5 max-h-24 overflow-y-auto rounded-lg bg-muted/30 border border-border p-2">
                    {availableKeys.map((key) => (
                      <button
                        key={key}
                        type="button"
                        onClick={() => insertReference(key)}
                        className="inline-flex items-center gap-1 rounded-md bg-muted hover:bg-accent border border-border px-2 py-1 text-xs font-mono text-foreground transition-colors"
                      >
                        <Link2 className="size-2.5 text-muted-foreground" />
                        {key}
                      </button>
                    ))}
                  </div>
                </div>
              )}

              {/* Error */}
              {formError && (
                <p className="text-sm text-destructive bg-destructive/10 border border-destructive/30 rounded-lg px-3 py-2 break-words">
                  {formError}
                </p>
              )}

              <DialogFooter>
                <DialogClose render={<Button type="button" variant="outline">Cancel</Button>} />
                <Button type="submit" disabled={isPending}>
                  {isPending
                    ? 'Saving...'
                    : dialog.mode === 'create'
                      ? 'Add Variable'
                      : 'Save Changes'}
                </Button>
              </DialogFooter>
            </form>
          </DialogContent>
        </Dialog>
      </div>

      {/* Project + Environment selectors */}
      <Card>
        <CardHeader className="border-b">
          <CardTitle className="text-base">Filter</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-wrap gap-3 pt-4">
          <div className="flex-1 min-w-[160px]">
            <Label className="text-xs text-muted-foreground mb-1.5 block">Project</Label>
            <Select
              value={selectedProject}
              onValueChange={(val) => {
                setSelectedProject(val || '');
                setSelectedEnv('');
              }}
            >
              <SelectTrigger>
                <SelectValue placeholder="All projects" />
              </SelectTrigger>
              <SelectContent>
                {projects?.map((p) => (
                  <SelectItem key={p} value={p}>{p}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex-1 min-w-[160px]">
            <Label className="text-xs text-muted-foreground mb-1.5 block">Environment</Label>
            <Select
              value={selectedEnv}
              onValueChange={(val) => setSelectedEnv(val || '')}
              disabled={!selectedProject}
            >
              <SelectTrigger>
                <SelectValue placeholder={selectedProject ? 'All envs' : 'Select project first'} />
              </SelectTrigger>
              <SelectContent>
                {environments?.map((e) => (
                  <SelectItem key={e} value={e}>{e}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </CardContent>
      </Card>

      {/* Env var list */}
      <Card>
        <CardHeader className="border-b">
          <CardTitle className="text-base">
            Variables
            {envVars && envVars.length > 0 && (
              <Badge variant="secondary" className="ml-2">{envVars.length}</Badge>
            )}
          </CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          {!envVars || envVars.length === 0 ? (
            <div className="px-5 py-12 text-center">
              <Lock className="size-8 text-muted-foreground/40 mx-auto mb-2" />
              <p className="text-sm text-muted-foreground">No env vars yet</p>
              <p className="text-xs text-muted-foreground/70 mt-1">
                Add variables to manage them per project and environment
              </p>
            </div>
          ) : (
            <div className="divide-y divide-border">
              {envVars.map((ev) => (
                <div key={ev.id} className="px-4 sm:px-5 py-3 hover:bg-accent/30 transition-colors">
                  <div className="flex items-center justify-between gap-3">
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2 flex-wrap">
                        <code className="text-sm font-mono font-medium text-foreground">{ev.key}</code>
                        <Badge variant="outline" className="text-[10px]">{ev.project}</Badge>
                        <Badge variant="secondary" className="text-[10px]">{ev.environment}</Badge>
                        {ev.is_reference && (
                          <Badge variant="secondary" className="text-[10px] gap-1 bg-primary/10 text-primary">
                            <Link2 className="size-2.5" />
                            ref
                          </Badge>
                        )}
                      </div>
                      <div className="flex items-center gap-2 mt-1">
                        <code className="text-xs text-muted-foreground font-mono break-all">
                          {showValues[ev.id] ? ev.value : '••••••••••••'}
                        </code>
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          onClick={() => toggleValue(ev.id)}
                          aria-label="Toggle value visibility"
                        >
                          {showValues[ev.id] ? <EyeOff /> : <Eye />}
                        </Button>
                        {/* Copy resolved value */}
                        {showValues[ev.id] && ev.resolved_value && ev.resolved_value !== ev.value && (
                          <Button
                            variant="ghost"
                            size="icon-xs"
                            onClick={() => copyToClipboard(ev.resolved_value!)}
                            aria-label="Copy resolved value"
                          >
                            {copiedUrl === ev.resolved_value ? <Check /> : <Copy />}
                          </Button>
                        )}
                      </div>
                      {/* Show resolved value below if different */}
                      {showValues[ev.id] && ev.resolved_value && ev.resolved_value !== ev.value && (
                        <div className="mt-1.5 flex items-start gap-1.5">
                          <span className="text-[10px] text-muted-foreground/60 mt-0.5 shrink-0">→</span>
                          <code className="text-xs text-primary/80 font-mono break-all">
                            {ev.resolved_value}
                          </code>
                        </div>
                      )}
                    </div>
                    <div className="flex items-center gap-0.5 shrink-0">
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        onClick={() => {
                          setFormError('');
                          setDialog({
                            open: true,
                            mode: 'edit',
                            editId: ev.id,
                            project: ev.project,
                            environment: ev.environment,
                            key: ev.key,
                            value: ev.value,
                          });
                        }}
                        aria-label="Edit variable"
                        className="text-muted-foreground hover:text-foreground"
                      >
                        <Pencil />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        onClick={() => setDeleteTarget(ev)}
                        aria-label="Delete variable"
                        className="text-muted-foreground hover:text-destructive"
                      >
                        <Trash2 />
                      </Button>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      {/* Export endpoint */}
      {selectedProject && (
        <Card>
          <CardHeader>
            <div className="flex items-center gap-2">
              <KeyRound className="size-4 text-primary" />
              <CardTitle>Export Endpoint</CardTitle>
            </div>
            <CardDescription>
              Fetch encrypted env vars with an API key. References are resolved. Decrypt locally — the server never sends plaintext.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="flex items-center gap-2">
              <Badge variant="default" className="font-mono shrink-0">GET</Badge>
              <code className="flex-1 min-w-0 text-xs text-muted-foreground font-mono break-all">
                {exportUrl}
              </code>
              <Button
                variant="outline"
                size="icon-sm"
                onClick={() => copyToClipboard(exportUrl)}
                aria-label="Copy URL"
                className="shrink-0"
              >
                {copiedUrl === exportUrl ? <Check /> : <Copy />}
              </Button>
            </div>
            <Separator />
            <div className="rounded-lg border border-border bg-muted/30 p-3">
              <div className="flex items-center gap-1.5 mb-2">
                <Terminal className="size-3.5 text-muted-foreground" />
                <span className="text-xs font-medium text-foreground">Decrypt locally (Python)</span>
              </div>
              <pre className="text-[11px] text-muted-foreground font-mono overflow-x-auto whitespace-pre-wrap break-all">
{decryptSnippet}
              </pre>
            </div>
          </CardContent>
        </Card>
      )}

      {/* How it works */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">How It Works</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3 text-sm text-muted-foreground">
          <div className="flex items-start gap-2">
            <span className="text-xs font-bold text-primary bg-primary/10 rounded px-1.5 py-0.5 shrink-0 mt-0.5">1</span>
            <p>
              <strong className="text-foreground">At rest:</strong> Values are encrypted in the database
              using NaCl secretbox with a server-side master key derived from{' '}
              <code className="text-foreground text-xs">ENCRYPTION_KEY</code> (or JWT secret).
            </p>
          </div>
          <div className="flex items-start gap-2">
            <span className="text-xs font-bold text-primary bg-primary/10 rounded px-1.5 py-0.5 shrink-0 mt-0.5">2</span>
            <p>
              <strong className="text-foreground">References:</strong> Use{' '}
              <code className="text-foreground text-xs">{'${KEY}'}</code> to reference another variable
              in the same project/environment. Use{' '}
              <code className="text-foreground text-xs">{'${KEY:-default}'}</code> for a fallback.
              Use{' '}
              <code className="text-foreground text-xs">{'$[project:env:var]'}</code> to reference
              a variable from a specific project and environment (e.g.{' '}
              <code className="text-foreground text-xs">{'$[myapp:dev:TOKEN]'}</code>).
              References are resolved on export — the raw value stays encrypted in the database.
            </p>
          </div>
          <div className="flex items-start gap-2">
            <span className="text-xs font-bold text-primary bg-primary/10 rounded px-1.5 py-0.5 shrink-0 mt-0.5">3</span>
            <p>
              <strong className="text-foreground">In transit:</strong> The export endpoint re-encrypts
              the JSON blob using the <strong className="text-foreground">API key</strong> as the encryption
              key (SHA-256 → 32-byte NaCl key). Only someone with the API key can decrypt it.
            </p>
          </div>
          <div className="flex items-start gap-2">
            <span className="text-xs font-bold text-primary bg-primary/10 rounded px-1.5 py-0.5 shrink-0 mt-0.5">4</span>
            <p>
              <strong className="text-foreground">Locally:</strong> Use PyNaCl or libsodium to decrypt —
              the response is <code className="text-foreground text-xs">base64(nonce + ciphertext)</code>.
            </p>
          </div>
        </CardContent>
      </Card>

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Delete Environment Variable"
        description="Are you sure you want to delete"
        itemName={deleteTarget?.key}
        confirmText="Delete Variable"
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
