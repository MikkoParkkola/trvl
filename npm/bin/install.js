#!/usr/bin/env node
"use strict";

const https = require("https");
const crypto = require("crypto");
const fs = require("fs");
const path = require("path");
const { execSync } = require("child_process");

const VERSION = require("../package.json").version;

// Release downloads start on github.com and redirect to GitHub's asset hosts.
// Nothing else may serve the binary (MIK-8073).
const ALLOWED_HOSTS = new Set([
  "github.com",
  "release-assets.githubusercontent.com",
  "objects.githubusercontent.com",
]);
const MAX_REDIRECTS = 5;

// checksums.txt is copied from the GitHub release into this package when it is
// published to npm, so the expected hash reaches the user through npm rather
// than through the same GitHub download it is meant to check.
const CHECKSUMS_PATH = path.join(__dirname, "..", "checksums.txt");

function getPlatform() {
  const platform = process.platform;
  if (platform === "darwin") return "darwin";
  if (platform === "linux") return "linux";
  if (platform === "win32") return "windows";
  throw new Error(`Unsupported platform: ${platform}`);
}

function getArch() {
  const arch = process.arch;
  if (arch === "x64") return "amd64";
  if (arch === "arm64") return "arm64";
  throw new Error(`Unsupported architecture: ${arch}`);
}

function isAllowedUrl(url) {
  let parsed;
  try {
    parsed = new URL(url);
  } catch {
    return false;
  }
  return parsed.protocol === "https:" && ALLOWED_HOSTS.has(parsed.hostname);
}

// expectedSha returns the SHA-256 listed for filename in a goreleaser
// checksums.txt ("<hex>  <name>" per line). The name must match exactly.
function expectedSha(checksumsText, filename) {
  for (const line of checksumsText.split(/\r?\n/)) {
    const m = line.trim().match(/^([0-9a-f]{64})\s+\*?(.+)$/i);
    if (m && m[2] === filename) return m[1].toLowerCase();
  }
  throw new Error(`no checksum for ${filename} in checksums.txt`);
}

function verifySha(buf, expected) {
  const actual = crypto.createHash("sha256").update(buf).digest("hex");
  if (actual !== expected) {
    throw new Error(`checksum mismatch: expected ${expected}, got ${actual}`);
  }
}

function download(url, get = https.get, redirects = 0) {
  return new Promise((resolve, reject) => {
    if (!isAllowedUrl(url)) {
      return reject(new Error(`${url} is not an allowed download host`));
    }
    get(url, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        res.resume();
        if (redirects >= MAX_REDIRECTS) {
          return reject(new Error(`too many redirects downloading ${url}`));
        }
        const next = new URL(res.headers.location, url).toString();
        return download(next, get, redirects + 1).then(resolve, reject);
      }
      if (res.statusCode !== 200) {
        res.resume();
        return reject(new Error(`Download failed: HTTP ${res.statusCode} from ${url}`));
      }
      const chunks = [];
      res.on("data", (chunk) => chunks.push(chunk));
      res.on("end", () => resolve(Buffer.concat(chunks)));
      res.on("error", reject);
    }).on("error", reject);
  });
}

function fail(message) {
  console.error(`\n${message}\n`);
  console.error("You can manually download from:");
  console.error(`  https://github.com/MikkoParkkola/trvl/releases/tag/v${VERSION}\n`);
  process.exit(1);
}

async function install() {
  const os = getPlatform();
  const arch = getArch();
  const filename = `trvl_${VERSION}_${os}_${arch}.tar.gz`;
  const url = `https://github.com/MikkoParkkola/trvl/releases/download/v${VERSION}/${filename}`;
  const binDir = path.join(__dirname);
  const binaryName = os === "windows" ? "trvl.exe" : "trvl";
  const binaryPath = path.join(binDir, binaryName);

  // Skip if binary already exists. It is not re-verified: verification covers
  // the download this script performs, not a file already on disk.
  if (fs.existsSync(binaryPath)) {
    console.log(`trvl binary already exists at ${binaryPath}`);
    return;
  }

  let expected;
  try {
    expected = expectedSha(fs.readFileSync(CHECKSUMS_PATH, "utf8"), filename);
  } catch (err) {
    fail(`Cannot verify the trvl download: ${err.message}`);
  }

  console.log(`Downloading trvl v${VERSION} for ${os}/${arch}...`);
  console.log(`  ${url}`);

  let tarball;
  try {
    tarball = await download(url);
    verifySha(tarball, expected);
  } catch (err) {
    fail(`Failed to download a verified trvl binary:\n  ${err.message}`);
  }

  // Write tarball to temp file and extract
  const tmpFile = path.join(binDir, filename);
  fs.writeFileSync(tmpFile, tarball);

  try {
    if (os === "windows") {
      // On Windows, use tar which is available since Windows 10
      execSync(`tar -xzf "${tmpFile}" -C "${binDir}" trvl.exe`, { stdio: "pipe" });
    } else {
      execSync(`tar -xzf "${tmpFile}" -C "${binDir}" trvl`, { stdio: "pipe" });
    }
  } catch (err) {
    console.error(`Failed to extract binary: ${err.message}`);
    process.exit(1);
  } finally {
    // Clean up tarball
    fs.unlinkSync(tmpFile);
  }

  // Make executable on Unix
  if (os !== "windows") {
    fs.chmodSync(binaryPath, 0o755);
  }

  console.log(`trvl v${VERSION} installed successfully.`);
}

if (require.main === module) {
  install();
}

module.exports = { expectedSha, verifySha, isAllowedUrl, download };
