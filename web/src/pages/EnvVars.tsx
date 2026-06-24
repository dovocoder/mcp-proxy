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
} from 'lucide-react';
import { envVars as envVarsApi, type EnvVar } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card';
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

export default function EnvVars() {
  const queryClient = useQueryClient();
  const [selectedProject, setSelectedProject] = useState<string>('');
  const [selectedEnv, setSelectedEnv] = useState<string>('');
  const [showCreate, setShowCreate] = useState(false);
  const [showValues, setShowValues] = useState<Record<string, boolean>>({});
  const [copiedUrl, setCopiedUrl] = useState<string | null>(null);
  const [newProject, setNewProject] = useState('');
  const [newEnv, setNewEnv] = useState('');
  const [newKey, setNewKey] = useState('');
  const [newValue, setNewValue] = useState('');
  const [createError, setCreateError] = useState('');

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
      setShowCreate(false);
      setNewProject('');
      setNewEnv('');
      setNewKey('');
      setNewValue('');
      setCreateError('');
    },
    onError: (err: Error) => setCreateError(err.message),
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

  const exportUrl = selectedProject
    ? `/api/env-vars/export?project=${encodeURIComponent(selectedProject)}${selectedEnv ? `&environment=${encodeURIComponent(selectedEnv)}` : ''}`
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
            Encrypted env vars per project & environment. Export via API key — decrypt locally.
          </p>
        </div>
        <Dialog open={showCreate} onOpenChange={setShowCreate}>
          <DialogTrigger render={
            <Button className="shrink-0">
              <Plus />
              <span className="hidden sm:inline">Add Variable</span>
              <span className="sm:hidden">Add</span>
            </Button>
          } />
          <DialogContent className="sm:max-w-md">
            <DialogHeader>
              <DialogTitle>Add Environment Variable</DialogTitle>
              <DialogDescription>
                Values are encrypted at rest with NaCl (Sodium) secretbox.
              </DialogDescription>
            </DialogHeader>
            <form
              onSubmit={(e) => {
                e.preventDefault();
                createMutation.mutate({
                  project: newProject,
                  environment: newEnv,
                  key: newKey,
                  value: newValue,
                });
              }}
              className="space-y-4"
            >
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-2">
                  <Label htmlFor="ev-project">Project</Label>
                  <Input
                    id="ev-project"
                    value={newProject}
                    onChange={(e) => setNewProject(e.target.value)}
                    placeholder="my-app"
                    required
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="ev-env">Environment</Label>
                  <Input
                    id="ev-env"
                    value={newEnv}
                    onChange={(e) => setNewEnv(e.target.value)}
                    placeholder="dev"
                    required
                  />
                </div>
              </div>
              <div className="space-y-2">
                <Label htmlFor="ev-key">Key</Label>
                <Input
                  id="ev-key"
                  value={newKey}
                  onChange={(e) => setNewKey(e.target.value)}
                  placeholder="DATABASE_URL"
                  required
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="ev-value">Value</Label>
                <Input
                  id="ev-value"
                  value={newValue}
                  onChange={(e) => setNewValue(e.target.value)}
                  placeholder="postgresql://..."
                  required
                />
              </div>
              {createError && (
                <p className="text-sm text-destructive bg-destructive/10 border border-destructive/30 rounded-lg px-3 py-2 break-words">
                  {createError}
                </p>
              )}
              <DialogFooter>
                <DialogClose render={<Button type="button" variant="outline">Cancel</Button>} />
                <Button type="submit" disabled={createMutation.isPending}>
                  {createMutation.isPending ? 'Adding...' : 'Add Variable'}
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
                <div key={ev.id} className="px-4 sm:px-5 py-3">
                  <div className="flex items-center justify-between gap-3">
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2 flex-wrap">
                        <code className="text-sm font-mono font-medium text-foreground">{ev.key}</code>
                        <Badge variant="outline" className="text-[10px]">{ev.project}</Badge>
                        <Badge variant="secondary" className="text-[10px]">{ev.environment}</Badge>
                      </div>
                      <div className="flex items-center gap-2 mt-1">
                        <code className="text-xs text-muted-foreground font-mono">
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
                      </div>
                    </div>
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      onClick={() => deleteMutation.mutate(ev.id)}
                      aria-label="Delete variable"
                      className="text-muted-foreground hover:text-destructive shrink-0"
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

      {/* Export endpoint */}
      {selectedProject && (
        <Card>
          <CardHeader>
            <div className="flex items-center gap-2">
              <KeyRound className="size-4 text-primary" />
              <CardTitle>Export Endpoint</CardTitle>
            </div>
            <CardDescription>
              Fetch encrypted env vars with an API key. Decrypt locally — the server never sends plaintext.
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
          <CardTitle className="text-base">How Encryption Works</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2 text-sm text-muted-foreground">
          <p>
            <strong className="text-foreground">At rest:</strong> Values are encrypted in the database
            using NaCl secretbox with a server-side master key derived from the JWT secret.
          </p>
          <p>
            <strong className="text-foreground">In transit:</strong> The export endpoint re-encrypts
            the JSON blob using the <strong className="text-foreground">API key</strong> as the encryption
            key (SHA-256 → 32-byte NaCl key). Only someone with the API key can decrypt it.
          </p>
          <p>
            <strong className="text-foreground">Locally:</strong> Use PyNaCl or libsodium to decrypt —
            the response is <code className="text-foreground">base64(nonce + ciphertext)</code>.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
