const API_BASE = '/api';

function getToken(): string | null {
  return localStorage.getItem('mcp_proxy_token');
}

export function setToken(token: string) {
  localStorage.setItem('mcp_proxy_token', token);
}

export function clearToken() {
  localStorage.removeItem('mcp_proxy_token');
}

export function isAuthenticated(): boolean {
  return !!getToken();
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const token = getToken();
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...((options.headers as Record<string, string>) || {}),
  };
  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }

  const res = await fetch(`${API_BASE}${path}`, { ...options, headers });
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(body.error || `HTTP ${res.status}`);
  }
  return res.json();
}

// --- Auth ---

export const auth = {
  login: (username: string, password: string) =>
    request<{ token: string; expires_at: string }>('/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }),
};

// --- Servers ---

export interface Server {
  id: string;
  name: string;
  transport: string;
  command?: string;
  args?: string[];
  url?: string;
  headers?: Record<string, string>;
  env?: Record<string, string>;
  timeout: number;
  connect_timeout: number;
  enabled: boolean;
  status: string;
  last_seen?: string;
  created_at: string;
  updated_at: string;
  tools_count?: number;
  live_error?: string;
}

export const servers = {
  list: () => request<Server[]>('/servers'),
  get: (id: string) => request<{ server: Server; tools_count: number; live_error: string }>(`/servers/${id}`),
  create: (data: Partial<Server>) =>
    request<Server>('/servers', { method: 'POST', body: JSON.stringify(data) }),
  update: (id: string, data: Partial<Server>) =>
    request<Server>(`/servers/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
  delete: (id: string) =>
    request<{ status: string }>(`/servers/${id}`, { method: 'DELETE' }),
  reconnect: (id: string) =>
    request<{ status: string }>(`/servers/${id}/reconnect`, { method: 'POST' }),
};

// --- API Keys ---

export interface APIKey {
  id: string;
  name: string;
  key_prefix: string;
  scopes: string[];
  active: boolean;
  last_used_at?: string;
  expires_at?: string;
  created_at: string;
}

export interface APIKeyWithSecret extends APIKey {
  key: string;
  message: string;
}

export const apiKeys = {
  list: () => request<APIKey[]>('/keys'),
  create: (data: { name: string; scopes: string[]; expires_in_days?: number }) =>
    request<APIKeyWithSecret>('/keys', { method: 'POST', body: JSON.stringify(data) }),
  delete: (id: string) =>
    request<{ status: string }>(`/keys/${id}`, { method: 'DELETE' }),
};

// --- Tools ---

export interface Tool {
  server_id: string;
  server_name: string;
  name: string;
  description: string;
  input_schema?: Record<string, unknown>;
}

export const tools = {
  list: () => request<Tool[]>('/tools'),
};

// --- Dashboard ---

export interface DashboardStats {
  total_servers: number;
  connected_servers: number;
  total_tools: number;
  total_api_keys: number;
}

export const dashboard = {
  stats: () => request<DashboardStats>('/dashboard'),
};
