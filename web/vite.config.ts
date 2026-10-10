/// <reference types="vitest/config" />

import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// The build goes into internal/web/dist, which the agora binary embeds (see docs/architecture/web.md).
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    assetsInlineLimit: 0, // fonts and icons stay files: the page's CSP allows no data: fonts
    modulePreload: { polyfill: false },
  },
  server: {
    // `pnpm dev` talks to a hub started with --web 8484
    proxy: { '/agora.v1.': 'http://127.0.0.1:8484' },
  },
  test: {
    environment: 'jsdom',
  },
});
