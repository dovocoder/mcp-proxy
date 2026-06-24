import { Routes, Route, Navigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import Layout from './components/Layout';
import Login from './pages/Login';
import Dashboard from './pages/Dashboard';
import ServersPage from './pages/Servers';
import ServerDetail from './pages/ServerDetail';
import CompoundServersPage from './pages/CompoundServers';
import APIKeysPage from './pages/APIKeys';
import ToolsPage from './pages/Tools';
import MemoriesPage from './pages/Memories';
import EnvVarsPage from './pages/EnvVars';
import { isAuthenticated, dashboard as dashboardApi } from './api/client';

function ProtectedRoute({ children }: { children: React.ReactNode }) {
  if (!isAuthenticated()) {
    return <Navigate to="/login" replace />;
  }
  return <>{children}</>;
}

export default function App() {
  const { data: stats } = useQuery({
    queryKey: ['dashboard'],
    queryFn: () => dashboardApi.stats(),
    enabled: isAuthenticated(),
  });

  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route
        path="/"
        element={
          <ProtectedRoute>
            <Layout stats={stats} />
          </ProtectedRoute>
        }
      >
        <Route index element={<Dashboard />} />
        <Route path="servers" element={<ServersPage />} />
        <Route path="servers/:id" element={<ServerDetail />} />
        <Route path="compounds" element={<CompoundServersPage />} />
        <Route path="compounds/:id" element={<CompoundServersPage />} />
        <Route path="keys" element={<APIKeysPage />} />
        <Route path="tools" element={<ToolsPage />} />
        <Route path="memories" element={<MemoriesPage />} />
        <Route path="env-vars" element={<EnvVarsPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  );
}
