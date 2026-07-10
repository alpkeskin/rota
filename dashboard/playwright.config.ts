import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  timeout: 30000,
  retries: 0,
  use: {
    baseURL: 'http://localhost',
    headless: false,
    screenshot: 'only-on-failure',
  },
  webServer: {
    command: 'echo "using existing Caddy server"',
    url: 'http://localhost',
    reuseExistingServer: true,
  },
});
