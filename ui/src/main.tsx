import { StrictMode, useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { get } from './api';
import { AppProvider } from './state';
import { App } from './App';
import { Login } from './pages/Login';
import type { Me } from './types';
import './styles.css';

function Root() {
  const [me, setMe] = useState<Me | null>(null);
  const [error, setError] = useState('');
  useEffect(() => {
    // A 401 here redirects to /login (see api()).
    get<Me>('/api/me').then(setMe).catch(e => setError(e instanceof Error ? e.message : String(e)));
  }, []);
  if (error) return <div className="msg bad" style={{ margin: 20 }}>Cannot load the dashboard: {error}</div>;
  if (!me) return null;
  return <AppProvider me={me}><App /></AppProvider>;
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>{location.pathname === '/login' ? <Login /> : <Root />}</StrictMode>,
);
