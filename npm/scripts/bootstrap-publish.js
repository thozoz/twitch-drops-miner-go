'use strict';

// One-time, human-run entry point. This script is written and syntax-checked
// as part of packaging automation, but it is NEVER invoked automatically —
// it performs a real, human-authenticated `npm publish`. See README/SUMMARY
// for the manual steps required to run it.

const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');

const mapping = require('./lib/mapping');
const packageArtifacts = require('./lib/package-artifacts');
const publishAll = require('./lib/publish-all');

function main() {
  const repoRoot = path.resolve(__dirname, '..', '..');

  const rawVersion = process.env.VERSION || '';
  const version = rawVersion.replace(/^v/, '');
  if (!version) {
    throw new Error('VERSION env var is required (e.g. VERSION=1.0.3 or VERSION=v1.0.3)');
  }
  const dryRun = process.env.DRY_RUN === 'true' || process.env.DRY_RUN === '1';

  let commit = 'none';
  try {
    commit = execFileSync('git', ['rev-parse', '--short', 'HEAD'], { cwd: repoRoot }).toString().trim();
  } catch (err) {
    commit = 'none';
  }
  const date = new Date().toISOString();

  const ldflags =
    `-s -w -X github.com/thozoz/twitch-drops-miner-go/pkg/version.Version=${version} ` +
    `-X github.com/thozoz/twitch-drops-miner-go/pkg/version.Commit=${commit} ` +
    `-X github.com/thozoz/twitch-drops-miner-go/pkg/version.Date=${date}`;

  const stagingDir = fs.mkdtempSync(path.join(os.tmpdir(), 'dropminer-bootstrap-'));

  const artifacts = [];
  for (const row of mapping) {
    const binName = row.platform === 'win32' ? 'tdm.exe' : 'tdm';
    const outPath = path.join(stagingDir, `${row.goos}_${row.goarch}`, binName);
    fs.mkdirSync(path.dirname(outPath), { recursive: true });

    console.log(`bootstrap-publish.js: building ${row.goos}/${row.goarch} -> ${outPath}`);
    execFileSync('go', ['build', '-ldflags', ldflags, '-o', outPath, './cmd/tdm'], {
      cwd: repoRoot,
      env: { ...process.env, CGO_ENABLED: '0', GOOS: row.goos, GOARCH: row.goarch },
      stdio: 'inherit',
    });

    artifacts.push({ goos: row.goos, goarch: row.goarch, path: outPath });
  }

  const npmRoot = path.join(repoRoot, 'npm');
  const result = packageArtifacts({ artifacts, version, npmRoot, repoRoot });
  console.log(`bootstrap-publish.js: stamped version ${result.version} into: ${result.updatedDirs.join(', ')}`);

  publishAll({ npmRoot, dryRun });

  const allPkgNames = ['@thozoz/dropminer', ...mapping.map((row) => row.pkgName)];
  console.log('');
  console.log('bootstrap-publish.js: done. For EACH of the following packages, configure Trusted');
  console.log('Publishing on npmjs.com -> Settings -> Trusted Publisher -> GitHub Actions:');
  console.log('  org/repo: thozoz/twitch-drops-miner-go');
  console.log('  workflow filename: release.yml');
  for (const name of allPkgNames) {
    console.log(`  - ${name}`);
  }
}

try {
  main();
} catch (err) {
  console.error(err.message);
  process.exit(1);
}
