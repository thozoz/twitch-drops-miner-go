'use strict';

// Single source of truth for the 6 supported GOOS/GOARCH -> npm platform
// package mapping. Every build-time script imports this table rather than
// re-deriving it. (The runtime shim in npm/dropminer/bin/tdm.js keeps its
// own small inline copy since it ships standalone inside the published
// package and cannot require a repo-internal path.)
module.exports = [
  {
    goos: 'linux',
    goarch: 'amd64',
    platform: 'linux',
    arch: 'x64',
    dir: 'dropminer-linux-x64',
    pkgName: '@thozoz/dropminer-linux-x64',
  },
  {
    goos: 'linux',
    goarch: 'arm64',
    platform: 'linux',
    arch: 'arm64',
    dir: 'dropminer-linux-arm64',
    pkgName: '@thozoz/dropminer-linux-arm64',
  },
  {
    goos: 'darwin',
    goarch: 'amd64',
    platform: 'darwin',
    arch: 'x64',
    dir: 'dropminer-darwin-x64',
    pkgName: '@thozoz/dropminer-darwin-x64',
  },
  {
    goos: 'darwin',
    goarch: 'arm64',
    platform: 'darwin',
    arch: 'arm64',
    dir: 'dropminer-darwin-arm64',
    pkgName: '@thozoz/dropminer-darwin-arm64',
  },
  {
    goos: 'windows',
    goarch: 'amd64',
    platform: 'win32',
    arch: 'x64',
    dir: 'dropminer-win32-x64',
    pkgName: '@thozoz/dropminer-win32-x64',
  },
  {
    goos: 'windows',
    goarch: 'arm64',
    platform: 'win32',
    arch: 'arm64',
    dir: 'dropminer-win32-arm64',
    pkgName: '@thozoz/dropminer-win32-arm64',
  },
];
