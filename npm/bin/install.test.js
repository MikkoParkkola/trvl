"use strict";

// MIK-8073: the postinstall must never install a binary it has not verified,
// and must not follow a download anywhere but GitHub's release hosts.
const test = require("node:test");
const assert = require("node:assert");
const crypto = require("node:crypto");
const { EventEmitter } = require("node:events");

const { expectedSha, verifySha, isAllowedUrl, download } = require("./install.js");

const sha = (buf) => crypto.createHash("sha256").update(buf).digest("hex");
const archive = Buffer.from("pretend tarball");
const name = "trvl_9.9.9_linux_amd64.tar.gz";
const checksums = `${sha(Buffer.from("other"))}  trvl_9.9.9_darwin_arm64.tar.gz\n${sha(archive)}  ${name}\n`;

test("expectedSha finds the line for the exact archive name", () => {
  assert.strictEqual(expectedSha(checksums, name), sha(archive));
});

test("expectedSha rejects an archive missing from checksums.txt", () => {
  assert.throws(() => expectedSha(checksums, "trvl_9.9.9_windows_amd64.tar.gz"), /no checksum/i);
});

test("expectedSha does not match a name that only ends with the archive name", () => {
  const sneaky = `${sha(archive)}  evil_${name}\n`;
  assert.throws(() => expectedSha(sneaky, name), /no checksum/i);
});

test("verifySha accepts the matching archive and rejects a tampered one", () => {
  assert.doesNotThrow(() => verifySha(archive, sha(archive)));
  assert.throws(() => verifySha(Buffer.from("tampered"), sha(archive)), /checksum mismatch/i);
});

test("isAllowedUrl allows only https on GitHub's release hosts", () => {
  for (const ok of [
    "https://github.com/MikkoParkkola/trvl/releases/download/v1/x.tar.gz",
    "https://release-assets.githubusercontent.com/github-production-release-asset/1/2?sig=x",
    "https://objects.githubusercontent.com/github-production-release-asset-2e65be/1/2",
  ]) {
    assert.ok(isAllowedUrl(ok), ok);
  }
  for (const bad of [
    "http://github.com/MikkoParkkola/trvl/releases/download/v1/x.tar.gz",
    "https://evil.example/x.tar.gz",
    "https://github.com.evil.example/x",
    "https://evilgithub.com/x",
    "not a url",
  ]) {
    assert.ok(!isAllowedUrl(bad), bad);
  }
});

// fakeGet answers each URL from a table: {status, location} or {status, body}.
function fakeGet(table) {
  const seen = [];
  const get = (url, cb) => {
    seen.push(url);
    const req = new EventEmitter();
    const entry = table[url];
    process.nextTick(() => {
      if (!entry) return req.emit("error", new Error("unexpected request " + url));
      const res = new EventEmitter();
      res.statusCode = entry.status;
      res.headers = entry.location ? { location: entry.location } : {};
      res.resume = () => {};
      cb(res);
      if (entry.body) res.emit("data", Buffer.from(entry.body));
      res.emit("end");
    });
    return req;
  };
  return { get, seen };
}

const start = "https://github.com/MikkoParkkola/trvl/releases/download/v9.9.9/" + name;

test("download follows a redirect to an allowed host", async () => {
  const asset = "https://release-assets.githubusercontent.com/a/b?sig=1";
  const { get } = fakeGet({ [start]: { status: 302, location: asset }, [asset]: { status: 200, body: "bytes" } });
  const buf = await download(start, get);
  assert.strictEqual(buf.toString(), "bytes");
});

test("download refuses a redirect to another host without requesting it", async () => {
  const evil = "https://evil.example/payload";
  const { get, seen } = fakeGet({ [start]: { status: 302, location: evil }, [evil]: { status: 200, body: "bad" } });
  await assert.rejects(download(start, get), /not an allowed download host/i);
  assert.deepStrictEqual(seen, [start], "only the GitHub URL may be requested; the off-host target never");
});

test("download refuses a downgrade to http", async () => {
  const plain = "http://github.com/MikkoParkkola/trvl/x";
  const { get } = fakeGet({ [start]: { status: 302, location: plain } });
  await assert.rejects(download(start, get), /not an allowed download host/i);
});

test("download stops after too many redirects", async () => {
  const table = {};
  let url = start;
  for (let i = 0; i < 10; i++) {
    const next = `https://github.com/MikkoParkkola/trvl/hop/${i}`;
    table[url] = { status: 302, location: next };
    url = next;
  }
  const { get } = fakeGet(table);
  await assert.rejects(download(start, get), /too many redirects/i);
});
