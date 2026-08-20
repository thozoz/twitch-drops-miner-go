'use strict';

const fs = require('node:fs');
const path = require('node:path');
const { execFileSync } = require('node:child_process');

const mapping = require('./mapping');

function publishAll({ npmRoot, dryRun = false, log = console.log }) {
  const dirs = [...mapping.map((row) => row.dir), 'dropminer'];

  for (const dir of dirs) {
    const pkgPath = path.join(npmRoot, dir, 'package.json');
    const { name, version } = JSON.parse(fs.readFileSync(pkgPath, 'utf8'));

    if (dryRun) {
      log(`[dry-run] would publish ${name}@${version}`);
      continue;
    }

    let alreadyPublished = false;
    try {
      execFileSync('npm', ['view', `${name}@${version}`, 'version'], { stdio: 'pipe' });
      alreadyPublished = true;
    } catch (err) {
      alreadyPublished = false;
    }

    if (alreadyPublished) {
      log(`${name}@${version} already published, skipping`);
      continue;
    }

    try {
      execFileSync('npm', ['publish', '--access', 'public'], {
        cwd: path.join(npmRoot, dir),
        stdio: 'inherit',
      });
    } catch (err) {
      const output = `${err.message || ''} ${err.stdout || ''} ${err.stderr || ''}`;
      if (output.includes('EPUBLISHCONFLICT') || output.includes('cannot publish over')) {
        log(`${name}@${version} publish conflict (already published by a concurrent run), skipping`);
        continue;
      }
      throw err;
    }
  }
}

module.exports = publishAll;
