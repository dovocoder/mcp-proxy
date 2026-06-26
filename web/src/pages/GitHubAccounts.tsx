import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Plus, Trash2, Github, Key, Link2, Check, AlertCircle } from 'lucide-react';
import { github as githubApi, type GitHubAccount } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Separator } from '@/components/ui/separator';
import { ConfirmDialog } from '@/components/ConfirmDialog';
import { InfoBanner } from '@/components/InfoBanner';
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
import { cn } from '@/lib/utils';

export default function GitHubAccounts() {
  const queryClient = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<GitHubAccount | null>(null);

  const { data: accounts } = useQuery({
    queryKey: ['github-accounts'],
    queryFn: githubApi.listAccounts,
  });

  const deleteMutation = useMutation({
    mutationFn: githubApi.deleteAccount,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['github-accounts'] });
    },
  });

  return (
    <div className="space-y-5">
      {/* Header */}
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-xl sm:text-2xl font-bold text-foreground">GitHub Accounts</h1>
          <p className="text-muted-foreground mt-1 text-sm">
            Link GitHub accounts with tokens to authenticate API calls (issue lookups, etc.)
          </p>
        </div>
        <Dialog open={createOpen} onOpenChange={setCreateOpen}>
          <DialogTrigger
            render={
              <Button size="sm" className="shrink-0">
                <Plus className="size-4" />
                <span className="hidden sm:inline">Add Account</span>
                <span className="sm:hidden">Add</span>
              </Button>
            }
          />
          <DialogContent className="sm:max-w-md">
            <AddAccountForm onSaved={() => setCreateOpen(false)} />
          </DialogContent>
        </Dialog>
      </div>

      {/* Info banner */}
      <InfoBanner
        icon={Github}
        title="Why link GitHub accounts?"
        description="Without a token, GitHub API calls are rate-limited to 60 requests/hour from the server IP. Linking a GitHub account with a personal access token bumps this to 5,000/hour and allows access to private repositories."
        iconColor="text-foreground"
        iconBg="bg-muted"
        tips={[
          { label: 'Token', explanation: 'A GitHub personal access token (classic or fine-grained). Stored encrypted at rest — never exposed via the API.' },
          { label: 'Env Var', explanation: 'Instead of pasting a token directly, reference an env var (e.g. GITHUB_TOKEN) that contains the token — useful for CI/CD or Docker deployments.' },
          { label: 'Multiple accounts', explanation: 'Add multiple accounts — the first available token is used automatically for API calls. Delete and re-add to change the order.' },
          { label: 'Scope', explanation: 'For public repos: no scopes needed. For private repos: grant "repo" scope on the token.' },
        ]}
      />

      {/* Account list */}
      {!accounts || accounts.length === 0 ? (
        <Card>
          <CardContent className="p-8 sm:p-12 text-center flex flex-col items-center gap-3">
            <Github className="size-10 text-muted-foreground/50" />
            <p className="text-muted-foreground text-sm">
              No GitHub accounts linked yet. Add one to authenticate API calls.
            </p>
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-3">
          {accounts.map((acct, idx) => (
            <Card key={acct.id}>
              <CardContent className="py-4">
                <div className="flex items-center justify-between gap-3">
                  <div className="flex items-center gap-3 min-w-0">
                    <div className="inline-flex items-center justify-center w-10 h-10 rounded-lg bg-muted shrink-0">
                      <Github className="size-5 text-foreground" />
                    </div>
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        <span className="font-medium text-foreground truncate">{acct.name}</span>
                        {idx === 0 && (
                          <Badge variant="secondary" className="text-[10px] bg-primary/10 text-primary">
                            active
                          </Badge>
                        )}
                      </div>
                      <div className="text-xs text-muted-foreground flex items-center gap-2 mt-0.5">
                        <span>@{acct.username}</span>
                        {acct.has_token ? (
                          <span className="flex items-center gap-0.5 text-emerald-400">
                            <Check className="size-3" />
                            {acct.token_env ? `env: ${acct.token_env}` : 'token set'}
                          </span>
                        ) : (
                          <span className="flex items-center gap-0.5 text-muted-foreground">
                            <AlertCircle className="size-3" />
                            no token
                          </span>
                        )}
                      </div>
                    </div>
                  </div>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    onClick={() => setDeleteTarget(acct)}
                    className="text-muted-foreground hover:text-destructive shrink-0"
                  >
                    <Trash2 className="size-4" />
                  </Button>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Delete GitHub Account"
        description="Delete"
        itemName={deleteTarget?.name}
        confirmText="Delete"
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

function AddAccountForm({ onSaved }: { onSaved: () => void }) {
  const queryClient = useQueryClient();
  const [name, setName] = useState('');
  const [username, setUsername] = useState('');
  const [token, setToken] = useState('');
  const [tokenEnv, setTokenEnv] = useState('');
  const [error, setError] = useState('');

  const useEnvVar = !!tokenEnv;

  const createMutation = useMutation({
    mutationFn: () => {
      const data: { name: string; username: string; token?: string; token_env?: string } = {
        name,
        username,
      };
      if (useEnvVar) {
        data.token_env = tokenEnv;
      } else {
        data.token = token;
      }
      return githubApi.createAccount(data);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['github-accounts'] });
      onSaved();
    },
    onError: (err: Error) => setError(err.message),
  });

  return (
    <>
      <DialogHeader>
        <DialogTitle>Link GitHub Account</DialogTitle>
        <DialogDescription>
          Add a GitHub account with a personal access token to authenticate API calls.
        </DialogDescription>
      </DialogHeader>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          setError('');
          createMutation.mutate();
        }}
        className="flex flex-col gap-4"
      >
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="gh-name">Display Name</Label>
            <Input
              id="gh-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Personal"
              required
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="gh-username">GitHub Username</Label>
            <Input
              id="gh-username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="octocat"
              required
            />
          </div>
        </div>

        {/* Token input — toggle between direct token and env var reference */}
        <div className="space-y-2">
          <Label>Authentication Method</Label>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={() => { setTokenEnv(''); }}
              className={cn(
                'flex-1 px-3 py-2 rounded-lg text-sm font-medium transition-colors border flex items-center justify-center gap-1.5',
                !useEnvVar
                  ? 'bg-primary text-primary-foreground border-primary'
                  : 'bg-transparent text-muted-foreground hover:bg-muted border-border'
              )}
            >
              <Key className="size-3.5" />
              Token
            </button>
            <button
              type="button"
              onClick={() => { setToken(''); setTokenEnv(tokenEnv || 'GITHUB_TOKEN'); }}
              className={cn(
                'flex-1 px-3 py-2 rounded-lg text-sm font-medium transition-colors border flex items-center justify-center gap-1.5',
                useEnvVar
                  ? 'bg-primary text-primary-foreground border-primary'
                  : 'bg-transparent text-muted-foreground hover:bg-muted border-border'
              )}
            >
              <Link2 className="size-3.5" />
              Env Var
            </button>
          </div>
        </div>

        {useEnvVar ? (
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="gh-token-env">Environment Variable Name</Label>
            <Input
              id="gh-token-env"
              value={tokenEnv}
              onChange={(e) => setTokenEnv(e.target.value)}
              placeholder="GITHUB_TOKEN"
              className="font-mono"
              required
            />
            <p className="text-xs text-muted-foreground">
              The proxy will read the token from this environment variable at runtime.
            </p>
          </div>
        ) : (
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="gh-token">Personal Access Token</Label>
            <Input
              id="gh-token"
              type="password"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder="ghp_..."
              className="font-mono"
              required
            />
            <p className="text-xs text-muted-foreground">
              Stored encrypted at rest — never exposed via the API.
            </p>
          </div>
        )}

        {error && (
          <p className="text-sm text-destructive">{error}</p>
        )}

        <DialogFooter>
          <DialogClose render={<Button variant="outline" type="button">Cancel</Button>} />
          <Button type="submit" disabled={createMutation.isPending || !name || !username || (!token && !tokenEnv)}>
            {createMutation.isPending ? 'Adding...' : 'Add Account'}
          </Button>
        </DialogFooter>
      </form>
    </>
  );
}
