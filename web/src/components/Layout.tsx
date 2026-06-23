import { NavLink, useNavigate, Outlet } from 'react-router-dom';
import { LayoutDashboard, Server, KeyRound, Wrench, LogOut, Network, Layers } from 'lucide-react';
import { clearToken } from '../api/client';

interface LayoutProps {
  stats?: {
    total_servers: number;
    connected_servers: number;
    total_tools: number;
    total_api_keys: number;
    total_compounds: number;
  };
}

export default function Layout({ stats }: LayoutProps) {
  const navigate = useNavigate();

  const handleLogout = () => {
    clearToken();
    navigate('/login');
  };

  const navItems = [
    { to: '/', label: 'Dashboard', icon: LayoutDashboard, end: true },
    { to: '/servers', label: 'Servers', icon: Server },
    { to: '/compounds', label: 'Compounds', icon: Layers },
    { to: '/keys', label: 'API Keys', icon: KeyRound },
    { to: '/tools', label: 'Tools', icon: Wrench },
  ];

  return (
    <div className="flex h-screen bg-slate-950">
      {/* Sidebar */}
      <aside className="w-64 bg-slate-900 border-r border-slate-800 flex flex-col">
        <div className="px-6 py-5 border-b border-slate-800">
          <div className="flex items-center gap-2">
            <Network className="w-7 h-7 text-brand-500" />
            <div>
              <h1 className="text-lg font-bold text-white">MCP Proxy</h1>
              <p className="text-xs text-slate-500">Gateway Management</p>
            </div>
          </div>
        </div>

        <nav className="flex-1 px-3 py-4 space-y-1">
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              className={({ isActive }) =>
                `flex items-center gap-3 px-3 py-2 rounded-lg text-sm font-medium transition-colors ${
                  isActive
                    ? 'bg-brand-600 text-white'
                    : 'text-slate-400 hover:text-white hover:bg-slate-800'
                }`
              }
            >
              <item.icon className="w-4 h-4" />
              {item.label}
            </NavLink>
          ))}
        </nav>

        {stats && (
          <div className="px-4 py-3 border-t border-slate-800 space-y-2">
            <div className="flex justify-between text-xs">
              <span className="text-slate-500">Servers</span>
              <span className="text-slate-300">
                {stats.connected_servers}/{stats.total_servers}
              </span>
            </div>
            <div className="flex justify-between text-xs">
              <span className="text-slate-500">Tools</span>
              <span className="text-slate-300">{stats.total_tools}</span>
            </div>
            <div className="flex justify-between text-xs">
              <span className="text-slate-500">API Keys</span>
              <span className="text-slate-300">{stats.total_api_keys}</span>
            </div>
            <div className="flex justify-between text-xs">
              <span className="text-slate-500">Compounds</span>
              <span className="text-slate-300">{stats.total_compounds}</span>
            </div>
          </div>
        )}

        <button
          onClick={handleLogout}
          className="flex items-center gap-3 px-3 py-2 mx-3 mb-4 rounded-lg text-sm font-medium text-slate-400 hover:text-white hover:bg-slate-800 transition-colors"
        >
          <LogOut className="w-4 h-4" />
          Logout
        </button>
      </aside>

      {/* Main content */}
      <main className="flex-1 overflow-auto">
        <div className="p-8">
          <Outlet />
        </div>
      </main>
    </div>
  );
}


