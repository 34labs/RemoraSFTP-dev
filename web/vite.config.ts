import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The built UI is emitted into ../internal/server/webassets so the Go
// binary can embed it with go:embed. No external CDNs are used.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/server/webassets',
    emptyOutDir: true,
    target: 'es2020',
    assetsDir: 'assets',
    sourcemap: false,
  },
  server: {
    port: 5180,
    // During `vite dev`, proxy API and WebSocket traffic to the local Go
    // engine so the frontend talks to the real backend.
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:7970',
        changeOrigin: false,
        ws: true,
      },
    },
  },
});
