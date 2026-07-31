#!/usr/bin/env node
// Patches typescript-eslint and @typescript-eslint/* packages to use
// TypeScript 6.x (installed as 'typescript-6') instead of TypeScript 7.x.
// TypeScript 7.0 removed the traditional JS API that typescript-eslint relies on.
// See https://github.com/typescript-eslint/typescript-eslint/issues/10940
import { existsSync, lstatSync, mkdirSync, rmSync, symlinkSync } from 'fs';
import { resolve, join } from 'path';
import { fileURLToPath } from 'url';

const __dirname = fileURLToPath(new URL('.', import.meta.url));
const nodeModules = resolve(__dirname, '..', 'node_modules');
const ts6Dir = join(nodeModules, 'typescript-6');

if (!existsSync(ts6Dir)) {
  // typescript-6 alias not installed (e.g. production install with --omit=dev)
  process.exit(0);
}

const pkgNames = [
  'typescript-eslint',
  '@typescript-eslint/typescript-estree',
  '@typescript-eslint/parser',
  '@typescript-eslint/eslint-plugin',
  '@typescript-eslint/type-utils',
  'ts-api-utils',
];

for (const pkg of pkgNames) {
  const pkgDir = join(nodeModules, pkg);
  if (!existsSync(pkgDir)) continue;

  const nestedModules = join(pkgDir, 'node_modules');
  const linkPath = join(nestedModules, 'typescript');

  if (!existsSync(nestedModules)) {
    mkdirSync(nestedModules, { recursive: true });
  }

  try {
    const stat = lstatSync(linkPath);
    if (stat.isSymbolicLink() || stat.isDirectory() || stat.isFile()) {
      rmSync(linkPath, { recursive: true, force: true });
    }
  } catch {
    // linkPath doesn't exist, nothing to remove
  }

  symlinkSync(ts6Dir, linkPath, 'junction');
}

console.log('Patched typescript-eslint packages to use TypeScript 6.x for linting.');
