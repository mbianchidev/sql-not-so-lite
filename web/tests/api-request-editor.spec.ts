import { test, expect } from './fixtures';
import type { Page } from '@playwright/test';
import { join } from 'node:path';

async function openFreshEditor(page: Page) {
  await page.goto('/');
  await page.getByRole('button', { name: 'Open workbench_demo', exact: true }).click();
  await page.getByRole('button', { name: 'Expose API', exact: true }).click();
  await page.getByRole('textbox', { name: 'API alias', exact: true }).fill('demo');
  await page.getByRole('checkbox', { name: 'Expose inventory', exact: true }).check();
  await page.getByRole('checkbox', { name: 'Allow writes', exact: true }).check();
  await page.getByRole('button', { name: 'Create API', exact: true }).click();
  await page.getByRole('button', { name: 'Open request editor', exact: true }).click();
}

test('sends scoped GET requests and shows exact status, headers and body', async ({ page, mock }) => {
  await openFreshEditor(page);
  await page.getByRole('button', { name: 'Send request', exact: true }).click();
  await expect(page.getByRole('status', { name: 'Response status' })).toContainText('200');
  await expect(page.getByRole('textbox', { name: 'Response body', exact: true })).toHaveValue(/9007199254740993/);
  await page.getByText('Response headers', { exact: true }).click();
  await expect(page.getByRole('textbox', { name: 'Response headers', exact: true })).toHaveValue(/x-fixture/);
  expect(mock.dataRequests[0].headers.authorization).toBe('Bearer synthetic-scoped-token-for-browser-tests');
  expect(mock.dataRequests[0].method).toBe('GET');
  expect(mock.dataRequests[0].body).toBeNull();
});

test('sends CRUD methods, custom headers and unrounded JSON bodies', async ({ page, mock }) => {
  await openFreshEditor(page);
  await page.getByRole('combobox', { name: 'Request method', exact: true }).selectOption('POST');
  await page.getByText('Edit request headers', { exact: true }).click();
  await page.getByRole('textbox', { name: 'Request headers', exact: true }).fill('{"X-Client":"fixture"}');
  await page.getByRole('textbox', { name: 'Request body', exact: true }).fill('{"stock":9007199254740993}');
  await page.getByRole('button', { name: 'Send request', exact: true }).click();
  await expect(page.getByRole('status', { name: 'Response status' })).toContainText('201');
  expect(mock.dataRequests.at(-1)).toMatchObject({ method: 'POST', body: '{"stock":9007199254740993}' });
  expect(mock.dataRequests.at(-1)?.headers['x-client']).toBe('fixture');
  await page.getByRole('combobox', { name: 'Request method', exact: true }).selectOption('PATCH');
  await page.getByRole('textbox', { name: 'Request URL', exact: true }).fill('/api/data/demo/inventory?id=eq.1');
  await page.getByRole('button', { name: 'Send request', exact: true }).click();
  await expect(page.getByRole('status', { name: 'Response status' })).toContainText('200');
  await page.getByRole('combobox', { name: 'Request method', exact: true }).selectOption('DELETE');
  await page.getByRole('button', { name: 'Send request', exact: true }).click();
  await expect(page.getByRole('status', { name: 'Response status' })).toContainText('204');
  await expect(page.getByText('No response body', { exact: true })).toBeVisible();
  expect(mock.dataRequests.at(-1)?.body).toBeNull();
});

test('rejects invalid input and prevents credential-bearing external requests', async ({ page, mock }) => {
  await openFreshEditor(page);
  await page.getByRole('combobox', { name: 'Request method', exact: true }).selectOption('POST');
  await page.getByRole('textbox', { name: 'Request body', exact: true }).fill('{invalid');
  await page.getByRole('button', { name: 'Send request', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('valid JSON');
  expect(mock.dataRequests).toHaveLength(0);
  await page.getByRole('textbox', { name: 'Request body', exact: true }).fill('{}');
  await page.getByRole('textbox', { name: 'Request URL', exact: true }).fill('https://example.invalid/api/data/demo/inventory');
  await page.getByRole('button', { name: 'Send request', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('same origin');
  expect(mock.dataRequests).toHaveLength(0);
  await page.getByRole('textbox', { name: 'Request URL', exact: true }).fill('/api/databases');
  await page.getByRole('button', { name: 'Send request', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('/api/data/');
  expect(mock.dataRequests).toHaveLength(0);
});

test('validates header values and the existing request-size limit', async ({ page, mock }) => {
  await openFreshEditor(page);
  await page.getByText('Edit request headers', { exact: true }).click();
  await page.getByRole('textbox', { name: 'Request headers', exact: true }).fill('{"X-Count":1}');
  await page.getByRole('button', { name: 'Send request', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('string value');
  await page.getByRole('textbox', { name: 'Request headers', exact: true }).fill('{"Authorization":"Bearer forbidden"}');
  await page.getByRole('button', { name: 'Send request', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('authentication controls');
  await page.getByRole('textbox', { name: 'Request headers', exact: true }).fill('{}');
  await page.getByRole('combobox', { name: 'Request method', exact: true }).selectOption('POST');
  await page.getByRole('textbox', { name: 'Request body', exact: true }).fill(JSON.stringify({ note: 'x'.repeat(1024 * 1024) }));
  await page.getByRole('button', { name: 'Send request', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('1 MiB API limit');
  expect(mock.dataRequests).toHaveLength(0);
});

test('visibly bounds large response bodies without hiding HTTP status', async ({ page }) => {
  await openFreshEditor(page);
  await page.route('**/api/data/demo/inventory?limit=50', (route) => route.fulfill({
    status: 200, headers: { 'content-type': 'text/plain' }, body: 'x'.repeat(1024 * 1024 + 10),
  }));
  await page.getByRole('button', { name: 'Send request', exact: true }).click();
  await expect(page.getByRole('status', { name: 'Response status' })).toContainText('200');
  await expect(page.getByRole('alert')).toContainText('truncated at 1 MiB');
  expect((await page.getByRole('textbox', { name: 'Response body', exact: true }).inputValue()).length).toBe(1024 * 1024);
});

test('opens saved APIs and displays HTTP errors without signing out the administrator', async ({ page, mock }) => {
  mock.authRequired = true;
  mock.exposures.push({ alias: 'demo', database_id: 10, managed_database: 'workbench_demo', tables: ['inventory'], writable: false });
  await page.goto('/');
  await page.getByLabel('Admin token', { exact: true }).fill(mock.adminToken);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await page.getByRole('button', { name: 'Open workbench_demo', exact: true }).click();
  await page.getByRole('button', { name: 'Expose API', exact: true }).click();
  await page.getByRole('button', { name: 'Open request editor for demo', exact: true }).click();
  await page.getByLabel('Request API token', { exact: true }).fill('invalid');
  await page.getByRole('button', { name: 'Send request', exact: true }).click();
  await expect(page.getByRole('status', { name: 'Response status' })).toContainText('401');
  await expect(page.getByRole('textbox', { name: 'Response body', exact: true })).toHaveValue(/valid API bearer token required/);
  await expect(page.getByRole('button', { name: 'Sign out', exact: true })).toBeVisible();
  expect(mock.dataRequests[0].headers.authorization).toBe('Bearer invalid');
  await page.getByRole('combobox', { name: 'Request authentication', exact: true }).selectOption('admin');
  await page.getByRole('button', { name: 'Send request', exact: true }).click();
  await expect(page.getByRole('status', { name: 'Response status' })).toContainText('200');
  expect(mock.dataRequests.at(-1)?.headers.authorization).toBe(`Bearer ${mock.adminToken}`);
  await page.getByRole('combobox', { name: 'Request method', exact: true }).selectOption('DELETE');
  await page.getByRole('button', { name: 'Send request', exact: true }).click();
  await expect(page.getByRole('status', { name: 'Response status' })).toContainText('403');
});

test('cancels in-flight requests and surfaces network failures', async ({ page }) => {
  await openFreshEditor(page);
  let release = () => {};
  const pending = new Promise<void>((resolve) => { release = resolve; });
  await page.route('**/api/data/demo/inventory?limit=50', async (route) => {
    await pending;
    await route.fulfill({ status: 200, json: [] });
  });
  await page.getByRole('button', { name: 'Send request', exact: true }).click();
  await page.getByRole('button', { name: 'Cancel request', exact: true }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Request cancelled' })).toBeVisible();
  release();
  await page.unroute('**/api/data/demo/inventory?limit=50');
  await page.route('**/api/data/demo/inventory?limit=50', (route) => route.abort('failed'));
  await page.getByRole('button', { name: 'Send request', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('Request failed');
});

test('renders request and response controls on desktop and mobile', async ({ page }) => {
  for (const [device, width, height] of [['desktop', 1440, 940], ['mobile', 390, 844]] as const) {
    await page.setViewportSize({ width, height });
    if (device === 'mobile') {
      await page.goto('/');
      await page.getByRole('button', { name: 'Open database navigation', exact: true }).click();
      await page.getByRole('button', { name: 'Open workbench_demo', exact: true }).click();
      await page.getByRole('button', { name: 'Expose API', exact: true }).click();
      await page.getByRole('textbox', { name: 'API alias', exact: true }).fill('demo');
      await page.getByRole('checkbox', { name: 'Expose inventory', exact: true }).check();
      await page.getByRole('button', { name: 'Create API', exact: true }).click();
      await page.getByRole('button', { name: 'Open request editor', exact: true }).click();
    } else await openFreshEditor(page);
    await page.getByRole('button', { name: 'Send request', exact: true }).click();
    await expect(page.getByRole('status', { name: 'Response status' })).toContainText('200');
    const editor = page.getByRole('region', { name: 'API request editor', exact: true });
    await editor.scrollIntoViewIfNeeded();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    if (process.env.SQNSL_EDITOR_CAPTURE_DIR) await editor.screenshot({ path: join(process.env.SQNSL_EDITOR_CAPTURE_DIR, `api-editor-${device}.png`), animations: 'disabled' });
  }
});
