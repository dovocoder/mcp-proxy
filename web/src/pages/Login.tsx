import { useState, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { auth, setToken } from '../api/client';
import { Card, CardHeader, CardTitle, CardDescription, CardContent, CardFooter } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Separator } from '@/components/ui/separator';

export default function Login() {
  const navigate = useNavigate();
  const [username, setUsername] = useState('admin');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [oidcEnabled, setOidcEnabled] = useState(false);
  const [passwordLoginEnabled, setPasswordLoginEnabled] = useState(true);

  // Check for OIDC token in URL fragment (from OIDC callback redirect)
  // Fragments (#) aren't sent to the server, so they don't leak in logs/referrer
  useEffect(() => {
    const hash = window.location.hash;
    const tokenMatch = hash.match(/[#&]token=([^&]+)/);
    if (tokenMatch) {
      const token = decodeURIComponent(tokenMatch[1]);
      setToken(token);
      // Clean the URL fragment
      window.location.hash = '';
      navigate('/', { replace: true });
      return;
    }
    // Check auth configuration
    auth.oidcStatus()
      .then((res) => {
        setOidcEnabled(res.enabled);
        setPasswordLoginEnabled(res.password_login);
      })
      .catch(() => {});
  }, [navigate]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError('');
    try {
      const res = await auth.login(username, password);
      setToken(res.token);
      navigate('/');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Login failed');
    } finally {
      setLoading(false);
    }
  };

  const handleOIDC = () => {
    window.location.href = '/api/auth/oidc/login';
  };

  return (
    <div className="min-h-screen bg-background flex items-center justify-center px-4 py-8">
      <div className="w-full max-w-md">
        <div className="text-center mb-6 sm:mb-8">
          <h1 className="text-2xl font-bold text-foreground">MCP Proxy</h1>
          <p className="text-sm text-muted-foreground mt-1">Gateway Management Console</p>
        </div>

        <Card>
          <CardHeader>
            <CardTitle>Sign in</CardTitle>
            <CardDescription>
              {oidcEnabled
                ? 'Sign in with your OIDC provider or local credentials'
                : 'Enter your credentials to access the console'}
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            {/* OIDC login button */}
            {oidcEnabled && (
              <>
                <Button
                  variant="default"
                  className="w-full"
                  onClick={handleOIDC}
                  disabled={loading}
                >
                  Continue with SSO
                </Button>
                {passwordLoginEnabled && (
                  <div className="flex items-center gap-3">
                    <Separator className="flex-1" />
                    <span className="text-xs text-muted-foreground">or</span>
                    <Separator className="flex-1" />
                  </div>
                )}
              </>
            )}

            {/* Password login form (hidden when disabled) */}
            {passwordLoginEnabled && (
              <form onSubmit={handleSubmit} className="space-y-4">
                <div className="space-y-2">
                  <Label htmlFor="username">Username</Label>
                  <Input
                    id="username"
                    type="text"
                    value={username}
                    onChange={(e) => setUsername(e.target.value)}
                    placeholder="admin"
                    autoComplete="username"
                    required
                  />
                </div>

                <div className="space-y-2">
                  <Label htmlFor="password">Password</Label>
                  <Input
                    id="password"
                    type="password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    placeholder="••••••••"
                    autoComplete="current-password"
                    required
                  />
                </div>

                {error && (
                  <div className="flex items-start gap-2">
                    <Badge variant="destructive" className="break-words whitespace-normal text-left leading-relaxed">
                      {error}
                    </Badge>
                  </div>
                )}

                <Button type="submit" disabled={loading} variant={oidcEnabled ? "outline" : "default"} className="w-full">
                  {loading ? 'Signing in...' : 'Sign In'}
                </Button>
              </form>
            )}

            {/* SSO-only message */}
            {!passwordLoginEnabled && oidcEnabled && (
              <p className="text-center text-sm text-muted-foreground py-2">
                Password login is disabled. Use SSO to continue.
              </p>
            )}
          </CardContent>
        </Card>

        {passwordLoginEnabled && !oidcEnabled && (
          <p className="text-center text-xs text-muted-foreground mt-5 sm:mt-6 px-4">
            Default credentials: admin / admin (set MCP_PROXY_ADMIN_PASS to change)
          </p>
        )}
      </div>
    </div>
  );
}
