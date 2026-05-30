#!/usr/bin/env node

'use strict';

const { spawnSync } = require('child_process');
const path = require('path');
const fs = require('fs');

const platform = process.platform;
const arch = process.arch;

let binaryPath;

if (platform === 'darwin' && arch === 'arm64') {
  binaryPath = path.join(__dirname, '..', 'vendor', 'darwin-arm64', 'xit');
} else if (platform === 'darwin' && arch === 'x64') {
  binaryPath = path.join(__dirname, '..', 'vendor', 'darwin-amd64', 'xit');
} else if (platform === 'linux' && arch === 'x64') {
  binaryPath = path.join(__dirname, '..', 'vendor', 'linux-amd64', 'xit');
} else if (platform === 'linux' && arch === 'arm64') {
  binaryPath = path.join(__dirname, '..', 'vendor', 'linux-arm64', 'xit');
} else if (platform === 'win32' && arch === 'x64') {
  binaryPath = path.join(__dirname, '..', 'vendor', 'win32-amd64', 'xit.exe');
} else {
  process.stderr.write(`Unsupported platform/arch for xit: ${platform}/${arch}\n`);
  process.exit(1);
}

if (!fs.existsSync(binaryPath)) {
  process.stderr.write(`xit binary not found at: ${binaryPath}\n`);
  process.exit(1);
}

const result = spawnSync(binaryPath, process.argv.slice(2), {
  stdio: 'inherit',
  windowsHide: false,
});

if (result.error) {
  process.stderr.write(`Failed to run xit: ${result.error.message}\n`);
  process.exit(1);
}

process.exit(result.status ?? 0);
