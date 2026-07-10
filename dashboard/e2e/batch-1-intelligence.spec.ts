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
    // Set auth token via localStorage (key: auth_token)
    await page.goto(BASE_URL);
    await page.evaluate((token) => {
      localStorage.setItem('auth_token', token);
    }, authToken);
    // Navigate directly to proxies — middleware checks localStorage
    await page.goto(`${BASE_URL}/dashboard/proxies`);
    await page.waitForLoadState('networkidle');

    // Verify table headers contain new columns
    const headers = page.locator('th');
    const headerTexts = await headers.allTextContents();

    expect(headerTexts.some(t => t.includes('Speed'))).toBeTruthy();
    expect(headerTexts.some(t => t.includes('Error Type'))).toBeTruthy();
    expect(headerTexts.some(t => t.includes('Consec. Fails'))).toBeTruthy();

    // Verify table has data rows
    const rows = page.locator('tbody tr');
    const count = await rows.count();
    expect(count).toBeGreaterThan(0);

    // Verify first row has valid proxy address (IP:PORT format, not JS garbage)
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

  test('Sort by success_rate returns descending order', async ({ request }) => {
    const res = await request.get(
      `${BASE_URL}/api/v1/proxies?sort=success_rate&order=desc&limit=10`,
      { headers: { 'Authorization': `Bearer ${authToken}` } },
    );
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    const rates: number[] = body.proxies.map((p: any) => p.success_rate);
    expect(rates.length).toBeGreaterThan(1);
    for (let i = 1; i < rates.length; i++) {
      expect(rates[i]).toBeLessThanOrEqual(rates[i - 1]);
    }
  });
});
