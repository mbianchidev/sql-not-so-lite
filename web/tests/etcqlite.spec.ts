import { test, expect } from './fixtures';

test('disabled integration leaves ordinary discovery intact', async ({ page, mock }) => {
  await page.goto('/');
  await expect(page.getByRole('button', { name: 'Inspect inventory.sqlite', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Inspect Etcqlite metadata', exact: true })).toBeHidden();
  expect(mock.projectionRequests).toEqual([]);
});

test('requires metadata consent and reuses the protected read-only inspector', async ({ page, mock }) => {
  mock.etcqlite.enabled = true;
  mock.authRequired = true;
  await page.goto('/');
  await page.getByLabel('Admin token', { exact: true }).fill(mock.adminToken);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  const inspect = page.getByRole('button', { name: 'Inspect Etcqlite metadata', exact: true });
  await expect(inspect).toBeDisabled();
  expect(mock.projectionRequests).toEqual([]);
  await page.getByRole('checkbox', { name: 'I consent to metadata-only read-only inspection.', exact: true }).check();
  await inspect.focus();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('region', { name: 'Inspect Etcqlite fixture', exact: true })).toBeVisible();
  await expect(page.getByText('18446744073709551615', { exact: true })).toBeVisible();
  await expect(page.getByText('9223372036854775809', { exact: true })).toBeVisible();
  await expect(page.getByText('Captured revision 9007199254740993', { exact: true })).toBeVisible();
  expect(mock.projectionRequests).toEqual([{ ack_read_only_inspection: true, include_data: false, ack_sensitive_data: false }]);
  const inspector = page.getByRole('region', { name: 'Inspect Etcqlite fixture', exact: true });
  await expect(inspector.getByRole('button', { name: /key_metadata/ })).toBeVisible();
  await expect(inspector.getByRole('button', { name: /key_values/ })).toBeHidden();
  await expect(inspector.getByRole('button', { name: /Expose API/ })).toBeHidden();
  await expect(inspector.getByRole('tab', { name: 'History', exact: true })).toBeHidden();
  await expect(page.getByRole('button', { name: 'Replicate Etcqlite fixture', exact: true })).toBeHidden();
});

test('requires separate raw-data consent and removes displayed data on metadata refresh', async ({ page, mock }) => {
  mock.etcqlite.enabled = true;
  await page.goto('/');
  await page.getByRole('checkbox', { name: 'I consent to metadata-only read-only inspection.', exact: true }).check();
  await page.getByRole('button', { name: 'Inspect Etcqlite metadata', exact: true }).click();
  const loadData = page.getByRole('button', { name: 'Load sensitive keys and values', exact: true });
  await expect(loadData).toBeDisabled();
  await page.getByRole('checkbox', { name: 'I consent to loading sensitive raw keys and values.', exact: true }).check();
  await loadData.click();
  await page.getByRole('button', { name: /key_values.*1 rows/ }).click();
  await expect(page.getByRole('cell', { name: 'hex:00ff01', exact: true })).toBeVisible();
  await expect(page.getByRole('cell', { name: 'hex:0002feff', exact: true })).toBeVisible();
  await expect(page.getByText(/Sensitive projection active/)).toBeVisible();
  expect(mock.projectionRequests.at(-1)).toEqual({ ack_read_only_inspection: true, include_data: true, ack_sensitive_data: true });
  await page.getByRole('button', { name: 'Refresh metadata', exact: true }).click();
  await expect(page.getByRole('cell', { name: 'hex:0002feff', exact: true })).toBeHidden();
  await expect(page.getByRole('button', { name: /key_values.*1 rows/ })).toBeHidden();
  await expect(loadData).toBeDisabled();
  expect(mock.projectionRequests.at(-1)).toEqual({ ack_read_only_inspection: true, include_data: false, ack_sensitive_data: false });
});

test('shows stale or disconnected projections without claiming live data', async ({ page, mock }) => {
  mock.etcqlite.enabled = true;
  await page.goto('/');
  await page.getByRole('checkbox', { name: 'I consent to metadata-only read-only inspection.', exact: true }).check();
  await page.getByRole('button', { name: 'Inspect Etcqlite metadata', exact: true }).click();
  mock.etcqlite.state = 'stale';
  await page.getByRole('button', { name: 'Refresh discovered databases', exact: true }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Stale projection' })).toBeVisible();
  mock.etcqlite.state = 'disconnected';
  await page.getByRole('button', { name: 'Refresh discovered databases', exact: true }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Disconnected' })).toBeVisible();
  await expect(page.getByRole('status').filter({ hasText: 'Daemon connected' })).toBeVisible();
  await expect(page.getByText(/Not a backup/)).toBeVisible();
});

test('failed metadata refresh does not retain visible sensitive rows', async ({ page, mock }) => {
  mock.etcqlite.enabled = true;
  await page.goto('/');
  await page.getByRole('checkbox', { name: 'I consent to metadata-only read-only inspection.', exact: true }).check();
  await page.getByRole('button', { name: 'Inspect Etcqlite metadata', exact: true }).click();
  await page.getByRole('checkbox', { name: 'I consent to loading sensitive raw keys and values.', exact: true }).check();
  await page.getByRole('button', { name: 'Load sensitive keys and values', exact: true }).click();
  await page.getByRole('button', { name: /key_values.*1 rows/ }).click();
  await expect(page.getByRole('cell', { name: 'hex:0002feff', exact: true })).toBeVisible();
  mock.failures.set('/api/etcqlite/projections', 'Etcqlite inspector stopped');
  await page.getByRole('button', { name: 'Refresh metadata', exact: true }).click();
  await expect(page.getByRole('alert').filter({ hasText: 'Etcqlite inspector stopped' })).toBeVisible();
  await expect(page.getByRole('cell', { name: 'hex:0002feff', exact: true })).toBeHidden();
});

test('sorts captured revisions without rounding large integers', async ({ page, mock }) => {
  mock.etcqlite.enabled = true;
  mock.inspectionRows.push(['2', '3', '4', '12', '9007199254740992', '3', '7']);
  await page.goto('/');
  await page.getByRole('checkbox', { name: 'I consent to metadata-only read-only inspection.', exact: true }).check();
  await page.getByRole('button', { name: 'Inspect Etcqlite metadata', exact: true }).click();
  await page.getByRole('button', { name: 'Sort by mod_revision', exact: true }).click();
  await expect(page.locator('.results-table tbody tr').first().getByRole('cell', { name: '9007199254740992', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Sort by mod_revision', exact: true }).click();
  await expect(page.locator('.results-table tbody tr').first().getByRole('cell', { name: '9007199254740993', exact: true })).toBeVisible();
});
