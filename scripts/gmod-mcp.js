#!/usr/bin/env node

const fs = require("fs");
const path = require("path");
const { spawn } = require("child_process");

if (process.platform !== "win32") {
  console.error("gmod-mcp only supports Windows.");
  process.exit(1);
}

const exe = path.join(__dirname, "..", "dist", "win32", "gmod_mcp.exe");

if (!fs.existsSync(exe)) {
  console.error(`Missing ${exe}. Reinstall the package so postinstall can download the release asset.`);
  process.exit(1);
}

const child = spawn(exe, process.argv.slice(2), {
  stdio: "inherit",
  windowsHide: true
});

child.on("exit", (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 0);
});
