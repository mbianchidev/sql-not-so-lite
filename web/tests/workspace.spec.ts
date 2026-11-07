import { join } from 'node:path';
import { test, expect } from './fixtures';

function contrastRatio(foreground: string, background: string): number {
  const luminance = (hex: string) => {
    if (/^#[0-9a-f]{3}$/i.test(hex)) {
      hex = `#${hex.slice(1).split('').map((channel) => channel.repeat(2)).join('')}`;
    }
    if (!/^#[0-9a-f]{6}$/i.test(hex)) throw new Error(`Unsupported test color: ${hex}`);
    const channels = [1, 3, 5].map((offset) => {
      const value = parseInt(hex.slice(offset, offset + 2), 16) / 255;
      return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
    });
    return channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722;
  };
  const a = luminance(foreground);
  const b = luminance(background);
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
}

test('maintains readable text contrast in both themes and hover states', async ({ page }) => {
  await page.goto('/');
  for (const theme of ['dark', 'light']) {
    if (theme === 'light') await page.getByRole('button', { name: 'Switch to light mode' }).click();
    const colors = await page.evaluate(() => {
      const style = getComputedStyle(document.documentElement);
      const read = (token: string) => style.getPropertyValue(token).trim();
      return {
        foregrounds: ['--text-primary', '--text-secondary', '--text-muted'].map(read),
        backgrounds: ['--bg-primary', '--bg-secondary', '--bg-tertiary', '--bg-hover'].map(read),
        accent: read('--accent'), accentText: read('--accent-text'),
      };
    });
    for (const foreground of colors.foregrounds) {
      for (const background of colors.backgrounds) {
        expect(contrastRatio(foreground, background), `${theme}: ${foreground} on ${background}`).toBeGreaterThanOrEqual(4.5);
      }
    }
    expect(contrastRatio(colors.accentText, colors.accent)).toBeGreaterThanOrEqual(4.5);
  }
});

test('keeps Monaco assets off the discovery startup path', async ({ page }) => {
  const editorRequests: string[] = [];
  page.on('request', (request) => {
    if (/\/assets\/(?:monaco|editor)[^/]*\.(?:js|css)(?:\?|$)/.test(request.url())) {
      editorRequests.push(request.url());
    }
  });
  await page.goto('/');
  await expect(page.getByRole('button', { name: 'Inspect inventory.sqlite', exact: true })).toBeVisible();
  expect(editorRequests).toEqual([]);
});

test('defaults to dark even with a light system preference and saves an explicit choice', async ({ page }) => {
  await page.emulateMedia({ colorScheme: 'light' });
  await page.goto('/');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await page.getByRole('button', { name: 'Switch to light mode' }).click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  await page.getByRole('button', { name: 'Switch to dark mode' }).click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
});

test('skips navigation and visibly focuses the main workspace', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('link', { name: 'Skip to main content', exact: true })).toBeVisible();
  await page.keyboard.press('Tab');
  await expect(page.getByRole('link', { name: 'Skip to main content', exact: true })).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('main')).toBeFocused();
  await expect(page.getByRole('main')).toHaveCSS('outline-style', 'solid');
});

test('searches managed databases and opens tables with the keyboard', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('searchbox', { name: 'Search managed databases' }).fill('workbench');
  await expect(page.getByRole('button', { name: 'Open analytics_demo', exact: true })).toBeHidden();
  const database = page.getByRole('button', { name: 'Open workbench_demo', exact: true });
  await database.focus();
  await page.keyboard.press('Enter');
  await page.getByRole('button', { name: 'Browse inventory table', exact: true }).click();
  await expect(page.getByRole('cell', { name: 'SKU-001', exact: true })).toBeVisible();
  await page.getByRole('tab', { name: 'Browse', exact: true }).focus();
  await page.keyboard.press('ArrowRight');
  await expect(page.getByRole('tab', { name: 'Schema', exact: true })).toHaveAttribute('aria-selected', 'true');
  await expect(page.getByRole('tabpanel', { name: 'Schema', exact: true })).toBeVisible();
});

test('creates and selects a database, then clears the selection after deletion', async ({ page, mock }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'New database', exact: true }).click();
  await page.getByRole('textbox', { name: 'Database name', exact: true }).fill('empty_demo');
  await page.getByRole('button', { name: 'Create database', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'empty_demo', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Open empty_demo', exact: true })).toHaveAttribute('aria-current', 'page');
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Drop empty_demo', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Discovery', exact: true })).toBeVisible();
  expect(mock.databases.some((database) => database.Name === 'empty_demo')).toBe(false);
});

test('inserts a row using SQLite defaults and sorts and exports visible data', async ({ page, mock }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Open workbench_demo', exact: true }).click();
  await page.getByRole('button', { name: 'Browse inventory table', exact: true }).click();
  await page.getByRole('button', { name: 'Add row', exact: true }).click();
  await page.getByRole('textbox', { name: 'sku value', exact: true }).fill('SKU-004');
  await page.locator('.row-composer').getByRole('button', { name: 'Add row', exact: true }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Row added with ID 4.' })).toBeVisible();
  expect(mock.inserts).toEqual([{ Columns: ['sku'], Values: ['SKU-004'] }]);
  await page.getByRole('button', { name: 'Sort by sku', exact: true }).click();
  await expect(page.getByRole('columnheader', { name: /sku/ })).toHaveAttribute('aria-sort', 'ascending');
  const download = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Export CSV', exact: true }).click();
  expect((await download).suggestedFilename()).toBe('query-result.csv');
});

test('loads SQL locally, matches the theme, and executes the latest text with the shortcut', async ({ page, mock }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Open workbench_demo', exact: true }).click();
  await page.getByRole('tab', { name: 'SQL Editor', exact: true }).click();
  const editor = page.locator('.monaco-editor').getByRole('textbox');
  await expect(editor).toBeVisible();
  await editor.focus();
  await page.keyboard.press('ControlOrMeta+a');
  await page.keyboard.insertText('SELECT 42 AS answer;');
  await page.keyboard.press('ControlOrMeta+Enter');
  await expect(page.getByRole('cell', { name: '42', exact: true })).toBeVisible();
  expect(mock.statements).toEqual(['SELECT 42 AS answer;']);
  await page.getByRole('button', { name: 'Switch to light mode' }).click();
  await expect(page.locator('.monaco-editor')).not.toHaveClass(/\bvs-dark\b/);
  await expect(page.locator('.monaco-editor .monaco-editor-background').first()).toHaveCSS('background-color', 'rgb(255, 255, 255)');
  await expect(page.getByRole('button', { name: 'SELECT 42 AS answer;', exact: true })).toBeVisible();
});

test('preserves schema creation and field order', async ({ page, mock }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Open workbench_demo', exact: true }).click();
  await page.getByRole('tab', { name: 'Schema', exact: true }).click();
  await page.getByRole('button', { name: 'New table', exact: true }).click();
  await page.getByRole('textbox', { name: 'Table name', exact: true }).fill('stock_history');
  await page.getByRole('textbox', { name: 'Field 1 name', exact: true }).fill('id');
  await page.getByRole('combobox', { name: 'Field 1 type', exact: true }).selectOption('INTEGER');
  await page.getByRole('checkbox', { name: 'Primary key', exact: true }).check();
  await page.getByRole('button', { name: 'Add field', exact: true }).click();
  await page.getByRole('textbox', { name: 'Field 2 name', exact: true }).fill('sku');
  await page.getByRole('button', { name: 'Move field 2 up', exact: true }).click();
  await expect(page.getByRole('textbox', { name: 'Field 1 name', exact: true })).toHaveValue('sku');
  await page.getByRole('button', { name: 'Create table', exact: true }).click();
  await expect(page.locator('.schema-details').getByRole('heading', { name: 'stock_history', exact: true })).toBeVisible();
  expect(mock.tables.at(-1)?.Columns.map((column) => column.Name)).toEqual(['sku', 'id']);
});

test('adds and edits schema fields without losing their settings', async ({ page, mock }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Open workbench_demo', exact: true }).click();
  await page.getByRole('tab', { name: 'Schema', exact: true }).click();
  await page.locator('.schema-tables').getByRole('button', { name: /inventory/ }).click();
  await expect(page.getByRole('cell', { name: 'Primary key', exact: true })).toBeVisible();
  await page.getByRole('textbox', { name: 'New field name', exact: true }).fill('state');
  await page.getByRole('textbox', { name: 'New field default', exact: true }).fill('pending');
  await page.locator('.schema-details').getByRole('button', { name: 'Add field', exact: true }).click();
  await page.getByRole('button', { name: 'Edit field state', exact: true }).click();
  await page.getByRole('textbox', { name: 'Edit state name', exact: true }).fill('status');
  await page.getByRole('textbox', { name: 'Edit state default', exact: true }).fill('ready');
  await page.locator('.schema-column-editor').getByRole('checkbox', { name: 'Not null', exact: true }).check();
  await page.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(page.getByRole('cell', { name: 'status', exact: true })).toBeVisible();
  expect(mock.fieldEdits).toEqual([{ OriginalName: 'state', Name: 'status', Type: 'TEXT', Nullable: false, DefaultValue: 'ready' }]);
});

test('filters discovery, preserves favorites, and opens the read-only inspector', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('searchbox', { name: 'Search discovered databases' }).fill('fixture-workspace');
  await expect(page.getByRole('button', { name: 'Inspect inventory.sqlite', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Inspect events.sqlite', exact: true })).toBeHidden();
  await page.getByRole('button', { name: 'Add inventory.sqlite to favorites', exact: true }).click();
  await page.getByRole('button', { name: 'Clear search', exact: true }).click();
  await page.getByRole('button', { name: /Favorites/ }).click();
  await expect(page.getByRole('button', { name: 'Inspect inventory.sqlite', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Inspect archive.sqlite', exact: true })).toBeHidden();
  await page.getByRole('button', { name: 'Inspect inventory.sqlite', exact: true }).click();
  await expect(page.getByRole('region', { name: 'Inspect inventory.sqlite', exact: true })).toBeVisible();
  await expect(page.getByText('Read-only inspector', { exact: true })).toBeVisible();
  await expect(page.getByRole('cell', { name: 'SKU-001', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Close inspector', exact: true }).click();
  await expect(page.getByRole('region', { name: 'Inspect inventory.sqlite', exact: true })).toBeHidden();
});

test('scans an explicit directory and preserves replication and snapshot actions', async ({ page, mock }) => {
  await page.goto('/');
  await page.getByRole('textbox', { name: 'Directory to scan', exact: true }).fill('/fixtures/workspace');
  await page.getByRole('button', { name: 'Scan directory', exact: true }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Scan complete: 3 databases found.' })).toBeVisible();
  expect(mock.scans).toEqual([['/fixtures/workspace']]);
  await page.getByRole('button', { name: 'Replicate inventory.sqlite', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Stop replication of inventory.sqlite', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Stop replication of inventory.sqlite', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Resume replication of inventory.sqlite', exact: true })).toBeVisible();
  page.once('dialog', async (dialog) => {
    expect(dialog.message()).toContain('local replica');
    await dialog.accept();
  });
  await page.getByRole('button', { name: 'Restore inventory.sqlite', exact: true }).click();
  await expect.poll(() => mock.restores).toEqual([1]);
  await page.getByRole('button', { name: 'Inspect inventory.sqlite', exact: true }).click();
  await page.getByRole('tab', { name: 'History', exact: true }).click();
  await expect(page.getByRole('button', { name: /Schema version 0/ })).toBeVisible();
  await page.getByRole('button', { name: /Schema version 0/ }).click();
  await expect(page.getByText('CREATE TABLE inventory (id INTEGER PRIMARY KEY);', { exact: true })).toBeVisible();
});

test('shows unavailable files and reports connection errors instead of live status', async ({ page, mock }) => {
  mock.failures.set('/api/stats', 'Daemon is unavailable');
  await page.goto('/');
  await expect(page.getByRole('status').filter({ hasText: 'Daemon unavailable' })).toBeVisible();
  await page.getByRole('button', { name: 'Inspect archive.sqlite', exact: true }).click();
  await expect(page.getByText('The source file is unavailable. Restore it to inspect its current contents.', { exact: true })).toBeVisible();
  await expect(page.getByRole('tab', { name: 'SELECT query', exact: true })).toBeDisabled();
});

test('reports schema failures without showing tables from the previous database', async ({ page, mock }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Open workbench_demo', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Browse inventory table', exact: true })).toBeVisible();
  mock.failures.set('/api/databases/analytics_demo/schema', 'Schema cannot be read');
  await page.getByRole('button', { name: 'Open analytics_demo', exact: true }).click();
  await expect(page.getByRole('alert').filter({ hasText: 'Schema cannot be read' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Browse inventory table', exact: true })).toBeHidden();
});

test('mobile navigation traps focus and closes after selecting a database', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  const trigger = page.getByRole('button', { name: 'Open database navigation', exact: true });
  await trigger.click();
  const navigation = page.getByRole('dialog', { name: 'Database navigation', exact: true });
  await expect(navigation).toBeVisible();
  for (const key of ['Tab', 'Shift+Tab']) {
    for (let step = 0; step < 12; step++) {
      await page.keyboard.press(key);
      expect(await navigation.evaluate((element) => element.contains(document.activeElement))).toBe(true);
    }
  }
  await page.keyboard.press('Escape');
  await expect(navigation).toBeHidden();
  await expect(trigger).toBeFocused();
  await trigger.click();
  await navigation.getByRole('button', { name: 'Open workbench_demo', exact: true }).click();
  await expect(navigation).toBeHidden();
  await expect(page.getByRole('heading', { name: 'workbench_demo', exact: true })).toBeVisible();
});

test('renders the complete workspace at desktop and mobile widths without page overflow', async ({ page }) => {
  const screenshots = process.env.SQNSL_CAPTURE_DIR;
  for (const [device, width, height] of [['desktop', 1440, 940], ['mobile', 390, 844]] as const) {
    await page.setViewportSize({ width, height });
    await page.goto('/');
    await expect(page.getByRole('button', { name: 'Inspect inventory.sqlite', exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    if (screenshots) await page.screenshot({ path: join(screenshots, `discovery-${device}.png`), fullPage: true, animations: 'disabled' });
    if (device === 'mobile') await page.getByRole('button', { name: 'Open database navigation', exact: true }).click();
    await page.getByRole('button', { name: 'Open workbench_demo', exact: true }).click();
    await page.getByRole('tab', { name: 'SQL Editor', exact: true }).click();
    await expect(page.locator('.monaco-editor')).toBeVisible();
    await page.locator('.monaco-editor').getByRole('textbox').focus();
    await page.keyboard.press('ControlOrMeta+a');
    await page.keyboard.insertText('SELECT id, sku, stock\nFROM inventory\nORDER BY id;');
    await page.getByRole('button', { name: 'Run query', exact: true }).click();
    await expect(page.getByRole('cell', { name: 'SKU-001', exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    if (screenshots) await page.screenshot({ path: join(screenshots, `workspace-${device}.png`), fullPage: true, animations: 'disabled' });
    if (screenshots && device === 'desktop') {
      await page.getByRole('tab', { name: 'Schema', exact: true }).click();
      await page.locator('.schema-tables').getByRole('button', { name: /inventory/ }).click();
      await expect(page.getByRole('button', { name: 'Edit field sku', exact: true })).toBeVisible();
      await page.screenshot({ path: join(screenshots, 'schema-desktop.png'), fullPage: true, animations: 'disabled' });
    }
  }
});
