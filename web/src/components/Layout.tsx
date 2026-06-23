import { useState } from 'react';
import { NavLink, useNavigate, Outlet } from 'react-router-dom';
import { LayoutDashboard, Server, KeyRound, Wrench, LogOut, Network, Layers, Menu } from 'lucide-react';
import { clearToken } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Sheet, SheetContent, SheetTrigger, SheetClose } from '@/components/ui/sheet';
import { cn } from '@/lib/utils';

interface LayoutProps {
  stats?: {
    total_servers: number;
    connected_servers: number;
    total_tools: number;
    total_api_keys: number;
    total_compounds: number;
  };
}

const navItems = [
  { to: '/', label: 'Dashboard', icon: LayoutDashboard, end: true },
  { to: '/servers', label: 'Servers', icon: Server, end: false },
  { to: '/compounds', label: 'Compounds', icon: Layers, end: false },
  { to: '/keys', label: 'API Keys', icon: KeyRound, end: false },
  { to: '/tools', label: 'Tools', icon: Wrench, end: false },
];

export default function Layout({ stats }: LayoutProps) {
  const navigate = useNavigate();
  const [sheetOpen, setSheetOpen] = useState(false);

  const handleLogout = () => {
    clearToken();
    navigate('/login');
  };

  return (
    <div className="min-h-screen bg-background flex flex-col lg:flex-row">
      {/* Desktop sidebar */}
      <aside className="hidden lg:flex w-60 bg-sidebar border-r border-sidebar-border flex-col sticky top-0 h-screen">
        <div className="px-5 py-4 border-b border-sidebar-border">
          <div className="flex items-center gap-2">
            <div className="flex items-center justify-center w-8 h-8 rounded-lg bg-primary text-primary-foreground">
              <Network className="w-4 h-4" />
            </div>
            <div>
              <h1 className="text-sm font-heading font-semibold text-sidebar-foreground">MCP Proxy</h1>
              <p className="text-xs text-muted-foreground">Gateway Management</p>
            </div>
          </div>
        </div>

        <nav className="flex-1 px-2 py-3 space-y-0.5">
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-2.5 px-3 py-2 rounded-lg text-sm font-medium transition-colors',
                  isActive
                    ? 'bg-sidebar-primary text-sidebar-primary-foreground'
                    : 'text-muted-foreground hover:text-sidebar-foreground hover:bg-sidebar-accent'
                )
              }
            >
              <item.icon className="size-4" />
              {item.label}
            </NavLink>
          ))}
        </nav>

        {stats && (
          <div className="px-3 py-3 border-t border-sidebar-border space-y-1.5">
            <div className="flex justify-between text-xs">
              <span className="text-muted-foreground">Servers</span>
              <span className="text-sidebar-foreground font-medium">
                {stats.connected_servers}/{stats.total_servers}
              </span>
            </div>
            <div className="flex justify-between text-xs">
              <span className="text-muted-foreground">Tools</span>
              <span className="text-sidebar-foreground font-medium">{stats.total_tools}</span>
            </div>
            <div className="flex justify-between text-xs">
              <span className="text-muted-foreground">API Keys</span>
              <span className="text-sidebar-foreground font-medium">{stats.total_api_keys}</span>
            </div>
            <div className="flex justify-between text-xs">
              <span className="text-muted-foreground">Compounds</span>
              <span className="text-sidebar-foreground font-medium">{stats.total_compounds}</span>
            </div>
          </div>
        )}

        <div className="p-2 border-t border-sidebar-border">
          <Button
            variant="ghost"
            onClick={handleLogout}
            className="w-full justify-start gap-2.5 text-muted-foreground hover:text-sidebar-foreground"
          >
            <LogOut className="size-4" />
            Logout
          </Button>
        </div>
      </aside>

      {/* Mobile header with Sheet */}
      <header className="lg:hidden flex items-center justify-between px-4 h-14 bg-card border-b border-border sticky top-0 z-40">
        <div className="flex items-center gap-2">
          <div className="flex items-center justify-center w-7 h-7 rounded-lg bg-primary text-primary-foreground">
            <Network className="w-4 h-4" />
          </div>
          <span className="font-heading font-semibold text-foreground">MCP Proxy</span>
        </div>
        <Sheet open={sheetOpen} onOpenChange={setSheetOpen}>
          <SheetTrigger
            render={<Button variant="ghost" size="icon" />}
          >
            <Menu className="size-5" />
          </SheetTrigger>
          <SheetContent side="left" className="w-64 p-0">
            <div className="px-5 py-4 border-b border-border">
              <div className="flex items-center gap-2">
                <div className="flex items-center justify-center w-8 h-8 rounded-lg bg-primary text-primary-foreground">
                  <Network className="w-4 h-4" />
                </div>
                <div>
                  <h1 className="text-sm font-heading font-semibold">MCP Proxy</h1>
                  <p className="text-xs text-muted-foreground">Gateway Management</p>
                </div>
              </div>
            </div>
            <nav className="px-2 py-3 space-y-0.5">
              {navItems.map((item) => (
                <NavLink
                  key={item.to}
                  to={item.to}
                  end={item.end}
                  onClick={() => setSheetOpen(false)}
                  className={({ isActive }) =>
                    cn(
                      'flex items-center gap-2.5 px-3 py-2 rounded-lg text-sm font-medium transition-colors',
                      isActive
                        ? 'bg-primary text-primary-foreground'
                        : 'text-muted-foreground hover:text-foreground hover:bg-accent'
                    )
                  }
                >
                  <item.icon className="size-4" />
                  {item.label}
                </NavLink>
              ))}
            </nav>
            <div className="p-2 border-t border-border mt-auto">
              <SheetClose render={
                <Button
                  variant="ghost"
                  onClick={handleLogout}
                  className="w-full justify-start gap-2.5 text-muted-foreground"
                />
              }>
                <LogOut className="size-4" />
                Logout
              </SheetClose>
            </div>
          </SheetContent>
        </Sheet>
      </header>

      {/* Main content */}
      <main className="flex-1 overflow-auto">
        <div className="p-4 sm:p-6 lg:p-8 max-w-6xl mx-auto pb-16 lg:pb-8">
          <Outlet />
        </div>
      </main>

      {/* Mobile bottom nav */}
      <nav className="lg:hidden fixed bottom-0 left-0 right-0 bg-card border-t border-border flex items-center justify-around h-14 z-40">
        {navItems.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            end={item.end}
            className={({ isActive }) =>
              cn(
                'flex flex-col items-center justify-center gap-0.5 flex-1 h-full transition-colors',
                isActive ? 'text-primary' : 'text-muted-foreground'
              )
            }
          >
            <item.icon className="size-5" />
            <span className="text-[10px] font-medium">{item.label}</span>
          </NavLink>
        ))}
      </nav>
    </div>
  );
}
