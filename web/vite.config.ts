/// <reference types="vitest/config" />

import { writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import react from '@vitejs/plugin-react';
import { defineConfig, type Plugin } from 'vite';

// The build goes into internal/web/dist, which the agora binary embeds (see docs/architecture/web.md).
const outDir = fileURLToPath(new URL('../internal/web/dist', import.meta.url));

// Git tracks only dist/.keep, so a checkout without a build still compiles; emptyOutDir removes
// it, so the build writes it back unchanged.
const keep = 'The web app is built here by make build-web; git tracks only this file.\n';
const keepDist: Plugin = {
  name: 'keep-dist',
  apply: 'build',
  closeBundle() {
    writeFileSync(join(outDir, '.keep'), keep);
  },
};

export default defineConfig({
  plugins: [react(), keepDist],
  build: {
    outDir,
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
