#!/usr/bin/env node

const fs = require("fs");
const https = require("https");
const path = require("path");

const repo = "Shaar-games/gmod_mcp";
const releaseTag =
  process.env.GMOD_MCP_RELEASE_TAG ||
  process.env.npm_config_gmod_mcp_release_tag ||
  "latest";
const assetNames = [
  process.env.GMOD_MCP_ASSET_NAME || process.env.npm_config_gmod_mcp_asset_name,
  "gmod_mcp_windows_386.exe",
  "gmod_mcp.exe"
].filter(Boolean);

const outDir = path.join(__dirname, "..", "dist", "win32");
const outFile = path.join(outDir, "gmod_mcp.exe");

if (process.platform !== "win32") {
  console.error("gmod-mcp only supports Windows.");
  process.exit(1);
}

fs.mkdirSync(outDir, { recursive: true });

if (fs.existsSync(outFile)) {
  process.exit(0);
}

downloadFirst(assetNames, 0).catch((err) => {
  console.error(err.message || err);
  process.exit(1);
});

async function downloadFirst(names, index) {
  if (index >= names.length) {
    throw new Error(
      `Could not download any known gmod_mcp release asset from ${repo}@${releaseTag}. ` +
        `Tried: ${names.join(", ")}`
    );
  }

  const name = names[index];
  const url = releaseUrl(releaseTag, name);
  try {
    await download(url, outFile);
    return;
  } catch (err) {
    if (fs.existsSync(outFile)) {
      fs.rmSync(outFile, { force: true });
    }
    if (err.statusCode === 404) {
      return downloadFirst(names, index + 1);
    }
    throw err;
  }
}

function releaseUrl(tag, assetName) {
  return `https://github.com/${repo}/releases/download/${tag}/${assetName}`;
}

function download(url, filePath, redirectsLeft = 5) {
  return new Promise((resolve, reject) => {
    https
      .get(
        url,
        {
          headers: {
            "User-Agent": "gmod-mcp-installer"
          }
        },
        (res) => {
          if (
            res.statusCode >= 300 &&
            res.statusCode < 400 &&
            res.headers.location &&
            redirectsLeft > 0
          ) {
            res.resume();
            download(res.headers.location, filePath, redirectsLeft - 1)
              .then(resolve)
              .catch(reject);
            return;
          }

          if (res.statusCode !== 200) {
            res.resume();
            const err = new Error(`Failed to download ${url}: HTTP ${res.statusCode}`);
            err.statusCode = res.statusCode;
            reject(err);
            return;
          }

          const file = fs.createWriteStream(filePath, { mode: 0o755 });
          res.pipe(file);
          file.on("finish", () => file.close(resolve));
          file.on("error", reject);
        }
      )
      .on("error", reject);
  });
}
