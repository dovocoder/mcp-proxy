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
    // On 401, clear the token and redirect to login
    if (res.status === 401) {
      clearToken();
      // Only redirect if we're not already on the login page
      if (!window.location.pathname.startsWith('/login')) {
        window.location.href = '/login';
      }
    }
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
  oidcStatus: () => request<{ enabled: boolean; password_login: boolean }>('/auth/oidc/status'),
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
  auth_token?: string;
  auth_method?: string;
  bearer_token_env?: string;
  enabled: boolean;
  logs_enabled: boolean;
  is_builtin?: boolean;
  timeout: number;
  connect_timeout: number;
  status: string;
  tools_count?: number;
  live_error?: string;
  created_at: string;
  updated_at: string;
}

export interface MemorySet {
  id: string;
  name: string;
  slug: string;
  description?: string;
  is_default: boolean;
  created_at: string;
}

export interface Memory {
  id: string;
  set_id: string;
  palace: string;
  room?: string;
  content: string;
  tags: string[];
  importance: number;
  access_count: number;
  created_at: string;
  updated_at: string;
  last_accessed?: string;
}

export interface LogEntry {
  timestamp: string;
  line: string;
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
  logs: (id: string) =>
    request<{ logs: LogEntry[]; count: number }>(`/servers/${id}/logs`),
  clearLogs: (id: string) =>
    request<{ status: string }>(`/servers/${id}/logs`, { method: 'DELETE' }),
  toggleLogs: (id: string, enabled: boolean) =>
    request<{ logs_enabled: boolean }>(`/servers/${id}/logs-enabled`, {
      method: 'PATCH',
      body: JSON.stringify({ logs_enabled: enabled }),
    }),
  initiateAuth: (id: string) =>
    request<{ auth_url: string; message: string }>(`/servers/${id}/auth`, { method: 'POST' }),
  authStatus: (id: string) =>
    request<{ status: string; has_tokens: boolean; expired: boolean; auth_method?: string; issuer?: string; authorization_endpoint?: string; device_auth_supported?: boolean; bearer_token_env?: string }>(`/servers/${id}/auth-status`),
  setBearerToken: (id: string, data: { method: string; bearer_token?: string; bearer_token_env?: string }) =>
    request<{ status: string }>(`/servers/${id}/bearer-token`, { method: 'POST', body: JSON.stringify(data) }),
  registration: (id: string) =>
    request<RegistrationInfo>(`/servers/${id}/registration`),
  deleteRegistration: (id: string) =>
    request<{ status: string }>(`/servers/${id}/registration`, { method: 'DELETE' }),
  initiateDeviceAuth: (id: string) =>
    request<DeviceAuthResult>(`/servers/${id}/device-auth`, { method: 'POST' }),
  pollDeviceAuth: (id: string) =>
    request<{ completed: boolean; expired: boolean }>(`/servers/${id}/device-auth/poll`, { method: 'POST' }),
  cancelDeviceAuth: (id: string) =>
    request<{ status: string }>(`/servers/${id}/device-auth`, { method: 'DELETE' }),
};

// --- Device Auth ---

export interface RegistrationInfo {
  status: 'none' | 'pre-registered' | 'registered' | 'cimd-available';
  issuer?: string;
  authorization_endpoint?: string;
  registration_endpoint?: string;
  client_id_metadata_document_supported?: boolean;
  client_id?: string;
  registration_method?: string;
  cimd_url?: string;
  created_at?: string;
}

export interface DeviceAuthResult {
  user_code: string;
  verification_uri: string;
  message: string;
  expires_in: number;
  interval: number;
}

// --- API Keys ---

export interface APIKey {
  id: string;
  name: string;
  key_prefix: string;
  scopes: string[];
  compound_id?: string | null;
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
  create: (data: { name: string; scopes: string[]; compound_id?: string; expires_in_days?: number }) =>
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

// --- Compound Servers ---

export interface CompoundServer {
  id: string;
  name: string;
  description?: string;
  dictionary_mode: boolean;
  created_at: string;
}

export interface CompoundServerWithMembers extends CompoundServer {
  members: Server[];
  tool_count: number;
  server_tool_count: number;
  memory_tool_count: number;
}

export const compounds = {
  list: () => request<CompoundServer[]>('/compounds'),
  get: (id: string) => request<CompoundServerWithMembers>(`/compounds/${id}`),
  create: (data: { name: string; description?: string; member_ids?: string[]; dictionary_mode?: boolean }) =>
    request<CompoundServer>('/compounds', { method: 'POST', body: JSON.stringify(data) }),
  update: (id: string, data: { name?: string; description?: string; dictionary_mode?: boolean }) =>
    request<CompoundServer>(`/compounds/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
  delete: (id: string) =>
    request<{ status: string }>(`/compounds/${id}`, { method: 'DELETE' }),
  addMember: (id: string, serverId: string) =>
    request<{ status: string }>(`/compounds/${id}/members/${serverId}`, { method: 'POST' }),
  removeMember: (id: string, serverId: string) =>
    request<{ status: string }>(`/compounds/${id}/members/${serverId}`, { method: 'DELETE' }),
};

// --- Dashboard ---

export interface DashboardStats {
  total_servers: number;
  connected_servers: number;
  total_tools: number;
  total_api_keys: number;
  total_compounds: number;
  total_memories: number;
}

export const dashboard = {
  stats: () => request<DashboardStats>('/dashboard'),
};

// --- Memories ---

export interface Palace {
  palace: string;
  count: number;
}

export const memories = {
  list: (setID: string, palace?: string) =>
    request<Memory[]>(`/memories?set_id=${setID}${palace ? `&palace=${encodeURIComponent(palace)}` : ''}`),
  get: (id: string) => request<Memory>(`/memories/${id}`),
  create: (data: { set_id?: string; palace?: string; room?: string; content: string; tags?: string[]; importance?: number }) =>
    request<Memory>('/memories', { method: 'POST', body: JSON.stringify(data) }),
  update: (id: string, data: Partial<{ palace: string; room: string; content: string; tags: string[]; importance: number }>) =>
    request<Memory>(`/memories/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
  delete: (id: string) =>
    request<{ status: string }>(`/memories/${id}`, { method: 'DELETE' }),
  palaces: (setID: string) => request<Palace[]>(`/memories/palaces?set_id=${setID}`),
  search: (setID: string, query: string) =>
    request<Memory[]>(`/memories/search?set_id=${setID}&q=${encodeURIComponent(query)}`),
};

// --- Memory Sets ---

export const memorySets = {
  list: () => request<MemorySet[]>('/memory-sets'),
  create: (data: { name: string; description?: string }) =>
    request<MemorySet>('/memory-sets', { method: 'POST', body: JSON.stringify(data) }),
  update: (id: string, data: { name?: string; description?: string }) =>
    request<MemorySet>(`/memory-sets/${id}`, { method: 'PATCH', body: JSON.stringify(data) }),
  delete: (id: string) =>
    request<{ status: string }>(`/memory-sets/${id}`, { method: 'DELETE' }),
};

// --- Registry ---

export interface RegistryServer {
  id?: string;
  name: string;
  description?: string;
  repository?: { url?: string; source?: string };
  version_detail?: {
    version?: string;
  };
  packages?: Array<{
    registry_type: string;
    registry_base_url?: string;
    identifier?: string;
    transport_type: string;
    command?: string;
    args?: string[];
    env?: Record<string, string>;
    url?: string;
  }>;
}

export const registry = {
  search: (query?: string) => {
    const qs = query ? `?q=${encodeURIComponent(query)}` : '';
    return fetch(`/api/registry/search${qs}`).then((r) => r.json());
  },
};

// --- Disabled Tools ---

export interface DisabledTool {
  id: string;
  tool_name: string;
  server_id?: string | null;
  created_at: string;
}

export const disabledTools = {
  list: (compoundId?: string) => {
    const qs = compoundId ? `?compound_id=${encodeURIComponent(compoundId)}` : '';
    return request<DisabledTool[]>(`/disabled-tools${qs}`);
  },
  create: (data: { tool_name: string; compound_id?: string }) =>
    request<DisabledTool>('/disabled-tools', { method: 'POST', body: JSON.stringify(data) }),
  delete: (id: string) =>
    request<{ status: string }>(`/disabled-tools/${id}`, { method: 'DELETE' }),
};

// --- Env Variables ---

export interface EnvVar {
  id: string;
  project: string;
  environment: string;
  key: string;
  value: string;
  resolved_value?: string;
  is_reference: boolean;
  created_at: string;
  updated_at: string;
}

export interface EnvVarExport {
  project: string;
  environment: string;
  encrypted: string;
  nonce_hint: string;
}

export const envVars = {
  list: (project?: string, environment?: string) => {
    const params = new URLSearchParams();
    if (project) params.set('project', project);
    if (environment) params.set('environment', environment);
    const qs = params.toString();
    return request<EnvVar[]>(`/env-vars${qs ? `?${qs}` : ''}`);
  },
  create: (data: { project: string; environment: string; key: string; value: string }) =>
    request<EnvVar>('/env-vars', { method: 'POST', body: JSON.stringify(data) }),
  update: (id: string, data: { value: string }) =>
    request<EnvVar>(`/env-vars/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
  delete: (id: string) =>
    request<{ status: string }>(`/env-vars/${id}`, { method: 'DELETE' }),
  projects: () => request<string[]>('/env-vars/projects'),
  environments: (project: string) =>
    request<string[]>(`/env-vars/environments?project=${encodeURIComponent(project)}`),
};
