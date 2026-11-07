import { test, expect } from './fixtures';
import { join } from 'node:path';

test('renders exposure settings on desktop and mobile without overflow', async ({ page }) => {
  for (const [device,width,height] of [['desktop',1440,940],['mobile',390,844]] as const) {
    await page.setViewportSize({ width, height });
    await page.goto('/');
    if (device === 'mobile') await page.getByRole('button', { name: 'Open database navigation', exact: true }).click();
    await page.getByRole('button', { name: 'Open workbench_demo', exact: true }).click();
    await page.getByRole('button', { name: 'Expose API', exact: true }).click();
    await page.getByRole('textbox', { name: 'API alias', exact: true }).fill('fixture_api');
    await page.getByRole('checkbox', { name: 'Expose inventory', exact: true }).check();
    await page.getByRole('button', { name: 'Create API', exact: true }).click();
    await expect(page.getByRole('textbox', { name: 'API token', exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    if (process.env.SQNSL_CRUD_CAPTURE_DIR) await page.screenshot({ path: join(process.env.SQNSL_CRUD_CAPTURE_DIR, `crud-${device}.png`), animations: 'disabled' });
  }
});

test('signs in with an admin token without saving it in browser storage', async ({ page, mock }) => {
  mock.authRequired = true;
  await page.goto('/');
  await page.getByLabel('Admin token', { exact: true }).fill('invalid');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('admin bearer token required');
  await page.getByLabel('Admin token', { exact: true }).fill(mock.adminToken);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Discovery', exact: true })).toBeVisible();
  expect(await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }))).not.toContain(mock.adminToken);
  await page.reload();
  await expect(page.getByLabel('Admin token', { exact: true })).toBeVisible();
});

test('exposes a managed database with scoped access and revokes it', async ({ page, mock }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Open workbench_demo', exact: true }).click();
  await page.getByRole('button', { name: 'Expose API', exact: true }).click();
  await page.getByRole('textbox', { name: 'API alias', exact: true }).fill('demo');
  await expect(page.getByRole('checkbox', { name: 'Allow writes', exact: true })).not.toBeChecked();
  await page.getByRole('checkbox', { name: 'Expose inventory', exact: true }).check();
  await page.getByRole('checkbox', { name: 'Allow writes', exact: true }).check();
  await page.getByRole('button', { name: 'Create API', exact: true }).click();
  await expect(page.getByRole('textbox', { name: 'API token', exact: true })).toHaveValue('synthetic-scoped-token-for-browser-tests');
  await expect(page.getByRole('textbox', { name: 'Example request', exact: true })).toHaveValue(/\/api\/data\/demo\/inventory/);
  expect(mock.exposures[0]).toMatchObject({ alias: 'demo', managed_database: 'workbench_demo', tables: ['inventory'], writable: true });
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Revoke API demo', exact: true }).click();
  await expect.poll(() => mock.exposures.length).toBe(0);
});

test('exposes a discovered file read-only without changing its inspector', async ({ page, mock }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Inspect inventory.sqlite', exact: true }).click();
  await page.getByRole('button', { name: 'Expose API', exact: true }).click();
  await page.getByRole('textbox', { name: 'API alias', exact: true }).fill('source');
  await page.getByRole('checkbox', { name: 'Expose inventory', exact: true }).check();
  await page.getByRole('button', { name: 'Create API', exact: true }).click();
  expect(mock.exposures[0]).toMatchObject({ alias: 'source', database_id: 1, writable: false });
  await expect(page.getByText('Read-only inspector', { exact: true })).toBeVisible();
});
