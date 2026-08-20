#!/usr/bin/env node
'use strict';

const path = require('node:path');
const { spawnSync } = require('node:child_process');

// Kept as an inline copy (not required from ../../scripts/lib/mapping.js) because
// this file ships standalone inside the published package — the repo-internal
// script path does not exist on an end user's machine.
const PLATFORMS = {
  'linux-x64': '@thozoz/dropminer-linux-x64',
  'linux-arm64': '@thozoz/dropminer-linux-arm64',
  'darwin-x64': '@thozoz/dropminer-darwin-x64',
  'darwin-arm64': '@thozoz/dropminer-darwin-arm64',
  'win32-x64': '@thozoz/dropminer-win32-x64',
  'win32-arm64': '@thozoz/dropminer-win32-arm64',
};

const key = `${process.platform}-${process.arch}`;
const pkgName = PLATFORMS[key];

if (!pkgName) {
  process.stderr.write(`tdm: unsupported platform ${key}\n`);
  process.exit(1);
}

let resolvedPath;
try {
  resolvedPath = require.resolve(`${pkgName}/package.json`);
} catch (err) {
  process.stderr.write(
    `tdm: could not find platform package "${pkgName}" — was it installed? ` +
      `(try reinstalling without --omit=optional)\n`
  );
  process.exit(1);
}

const pkgDir = path.dirname(resolvedPath);
const binName = process.platform === 'win32' ? 'tdm.exe' : 'tdm';
const binPath = path.join(pkgDir, 'bin', binName);

const result = spawnSync(binPath, process.argv.slice(2), { stdio: 'inherit' });

if (result.error) {
  process.stderr.write(`${result.error.message}\n`);
  process.exit(1);
}

process.exit(result.status ?? 1);
