import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The build goes into the Go package, which embeds it into the dashboard
// binary. `npm run dev` serves the UI with hot reload and forwards API calls
// to a dashboard running on :9400 (override with GREPALERT_DASHBOARD).
const target = process.env.GREPALERT_DASHBOARD || 'http://127.0.0.1:9400';

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/dashboard/web/dist',
    emptyOutDir: true,
    // No inline scripts: the dashboard's Content-Security-Policy forbids them.
    modulePreload: { polyfill: false },
    assetsInlineLimit: 0,
  },
  server: {
    proxy: {
      '/api': target,
      '/auth': target,
    },
  },
});
