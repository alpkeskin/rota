# Playwright E2E Setup — Batch 1 Verification

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Setup Playwright E2E test untuk verifikasi kolom intelligence (Speed, Error Type, Consec. Fails) di dashboard proxies page.

**Architecture:** Satu file test Playwright `e2e/batch-1-intelligence.spec.ts` — login via API, navigasi ke `/dashboard/proxies`, verifikasi 3 kolom baru muncul di header tabel, verifikasi data row pertama mengandung field intelligence.

**Tech Stack:** Playwright (sdh ada di `package.json`), TypeScript

## Global Constraints

- Playwright config: `playwright.config.ts` sudah ada di root
- Login via API call (bukan UI) buat speed
- Base URL: `http://localhost` (Caddy)
- Gunakan `data-testid` attribute kalau memungkinkan, kalau tidak — gunakan column header text

---

### Task 1: Install Playwright + Create Test File

**Files:**
- Create: `e2e/batch-1-intelligence.spec.ts`
- Modify: `test-batch-1.sh` (tambah playwright command)

**Interfaces:**
- Consumes: `dashboard/lib/types.ts` — Proxy interface dengan field intelligence
- Produces: Playwright test spec yang bisa dijalankan standalone

- [ ] **Step 1: Pastikan Playwright terinstall**

```bash
cd /path/to/rota
npx playwright install --with-deps chromium 2>&1 | tail -5
```

- [ ] **Step 2: Buat test file**

File: `e2e/batch-1-intelligence.spec.ts`

```typescript
import { test, expect } from '@playwright/test';

const BASE_URL = 'http://localhost';
const ADMIN = { username: 'admin', password: 'aa11aa' };

test.describe('Batch 1 — Error Classification + Intelligence Fields', () => {

  let authToken: string;

  test.beforeAll(async ({ request }) => {
    const res = await request.post(`${BASE_URL}/api/v1/auth/login`, {
      data: ADMIN,
      headers: { 'Content-Type': 'application/json' },
    });
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    authToken = body.token;
  });

  test('Proxy management table shows intelligence columns', async ({ page }) => {
    // Set auth cookie / localStorage via script
    await page.goto(BASE_URL);
    await page.evaluate((token) => {
      localStorage.setItem('rota_token', token);
    }, authToken);
    await page.goto(`${BASE_URL}/dashboard/proxies`);
    await page.waitForLoadState('networkidle');

    // Verify table headers
    const headers = page.locator('th');
    const headerTexts = await headers.allTextContents();

    expect(headerTexts.some(t => t.includes('Speed'))).toBeTruthy();
    expect(headerTexts.some(t => t.includes('Error Type'))).toBeTruthy();
    expect(headerTexts.some(t => t.includes('Consec. Fails'))).toBeTruthy();

    // Verify table has data rows
    const rows = page.locator('tbody tr');
    const count = await rows.count();
    expect(count).toBeGreaterThan(0);

    // Verify first row has valid proxy address (IP:PORT format)
    const firstAddr = await rows.first().locator('td').nth(1).textContent();
    expect(firstAddr).toMatch(/^\d+\.\d+\.\d+\.\d+:\d+$/);
  });

  test('API returns intelligence fields for all proxies', async ({ request }) => {
    const res = await request.get(`${BASE_URL}/api/v1/proxies?limit=5`, {
      headers: { 'Authorization': `Bearer ${authToken}` },
    });
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(body.proxies.length).toBeGreaterThan(0);

    for (const proxy of body.proxies) {
      expect(proxy).toHaveProperty('speed_tier');
      expect(proxy).toHaveProperty('error_type');
      expect(proxy).toHaveProperty('consecutive_fails');
      expect(proxy).toHaveProperty('recovery_attempt');
      // Address must be valid IP:PORT
      expect(proxy.address).toMatch(/^\d+\.\d+\.\d+\.\d+:\d+$/);
    }
  });

  test('Stats endpoint returns valid data', async ({ request }) => {
    const res = await request.get(`${BASE_URL}/api/v1/dashboard/stats`, {
      headers: { 'Authorization': `Bearer ${authToken}` },
    });
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(body.total_proxies).toBeGreaterThan(0);
    expect(body.active_proxies).toBeGreaterThanOrEqual(0);
    expect(body).toHaveProperty('avg_success_rate');
  });

  test('Sort by success_rate works', async ({ request }) => {
    const res = await request.get(
      `${BASE_URL}/api/v1/proxies?sort=success_rate&order=desc&limit=5`,
      { headers: { 'Authorization': `Bearer ${authToken}` } },
    );
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    const rates = body.proxies.map((p: any) => p.success_rate);
    // Check sorted descending
    for (let i = 1; i < rates.length; i++) {
      expect(rates[i]).toBeLessThanOrEqual(rates[i - 1]);
    }
  });
});
```

- [ ] **Step 3: Update README di test script**

Tambahkan di `test-batch-1.sh`, section E2E:

```bash
# 10. Playwright E2E (opsional — butuh browser)
if command -v npx &>/dev/null && npx playwright --version &>/dev/null 2>&1; then
  echo "" && echo "── E2E: Playwright ──"
  npx playwright test e2e/batch-1-intelligence.spec.ts --reporter=line 2>&1 | tail -15 \
    && pass "Playwright E2E tests" \
    || fail "Playwright E2E" "tests failed"
else
  echo "" && echo "── E2E: Playwright (skipped — not installed) ──"
fi
```

- [ ] **Step 4: Jalankan test**

```bash
npx playwright test e2e/batch-1-intelligence.spec.ts --reporter=line
```

Expected: 4 passed

- [ ] **Step 5: Commit**

```bash
git add e2e/batch-1-intelligence.spec.ts test-batch-1.sh
git commit -m "test(e2e): add Playwright E2E tests for Batch 1 intelligence fields"
```
