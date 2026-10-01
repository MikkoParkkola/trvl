import assert from "node:assert/strict";
import { createSign, generateKeyPairSync } from "node:crypto";
import test from "node:test";
import { locate } from "./geo.js";
import worker, { QUERIES, loadReport, render } from "./index.js";
import { frame, project } from "./map.js";

const { privateKey, publicKey } = generateKeyPairSync("rsa", { modulusLength: 2048 });
const publicJwk = publicKey.export({ format: "jwk" });
publicJwk.kid = "test-key";
publicJwk.alg = "RS256";
publicJwk.use = "sig";

function b64url(value) {
  return Buffer.from(value).toString("base64url");
}

function signJwt(payload, { kid = "test-key", key = privateKey } = {}) {
  const header = b64url(JSON.stringify({ alg: "RS256", kid, typ: "JWT" }));
  const body = b64url(JSON.stringify(payload));
  const data = `${header}.${body}`;
  const signature = createSign("RSA-SHA256").update(data).sign(key);
  return `${data}.${b64url(signature)}`;
}

const audience = "counts-aud";

function authorJwt(overrides = {}) {
  return signJwt({
    exp: Math.floor(Date.now() / 1000) + 3600,
    aud: audience,
    ...overrides,
  });
}

function pageEnv(extra = {}) {
  return {
    ACCESS_AUD: audience,
    TEST_ACCESS_KEYS: JSON.stringify([publicJwk]),
    ANALYTICS_READ_TOKEN: "sekret",
    ...extra,
  };
}

const report = {
  installs: "4",
  days: [{ day: "2026-10-01", installs: "4" }],
  places: [
    { country: "FR", city: "Paris", installs: "2" },
    { country: "", city: "", installs: "2" },
  ],
};

function sqlResult(rows) {
  return new Response(JSON.stringify({ data: rows }), {
    status: 200,
    headers: { "content-type": "application/json" },
  });
}

test("the queries count installs and do not select ids", () => {
  for (const sql of Object.values(QUERIES)) {
    assert.equal(sql.includes("count(DISTINCT index1)"), true);
    assert.equal(sql.includes("blob5"), false);
    assert.equal(/\bindex1\b/.test(sql.replaceAll("count(DISTINCT index1)", "")), true);
    assert.equal(sql.includes("probe-city-20261001"), true);
    assert.equal(sql.includes("SELECT index1"), false);
    assert.equal(sql.includes("SELECT blob5"), false);
  }
});

test("the page shows the total, the day, and the city, and not an id", () => {
  const html = render({
    ...report,
    places: [...report.places, { country: "NL", city: "<Utrecht>", installs: "1", install_id: "deadbeef" }],
  });
  assert.match(html, /4 installs in the last 90 days/);
  assert.match(html, /2026-10-01/);
  assert.match(html, /Paris, FR/);
  assert.match(html, /no city stored/);
  assert.match(html, /&lt;Utrecht&gt;, NL/);
  assert.equal(html.includes("deadbeef"), false);
  assert.equal(html.includes("install_id"), false);
});

test("a request without the access login is forbidden and does not query", async () => {
  let called = false;
  const previous = globalThis.fetch;
  globalThis.fetch = async () => {
    called = true;
    return sqlResult([]);
  };
  try {
    const response = await worker.fetch(new Request("https://counts.revaluator.ai/"), {
      ANALYTICS_READ_TOKEN: "sekret",
    });
    assert.equal(response.status, 403);
    assert.equal(called, false);
    assert.equal((await response.text()).includes("sekret"), false);
  } finally {
    globalThis.fetch = previous;
  }
});

test("an authorised request renders the sql rows", async () => {
  const seen = [];
  const previous = globalThis.fetch;
  globalThis.fetch = async (url, init) => {
    seen.push(String(init.body));
    assert.equal(init.headers.authorization, "Bearer sekret");
    if (String(init.body).includes("toDate")) return sqlResult(report.days);
    if (String(init.body).includes("blob6")) return sqlResult(report.places);
    return sqlResult([{ installs: "4", index1: "deadbeef" }]);
  };
  try {
    const response = await worker.fetch(
      new Request("https://counts.revaluator.ai/", {
        headers: { "cf-access-jwt-assertion": authorJwt() },
      }),
      pageEnv(),
    );
    assert.equal(response.status, 200);
    const html = await response.text();
    assert.match(html, /Paris, FR/);
    assert.equal(html.includes("deadbeef"), false);
    assert.equal(html.includes("sekret"), false);
    assert.equal(seen.length, 3);
  } finally {
    globalThis.fetch = previous;
  }
});

test("a forged, expired, or wrong-site login is forbidden and does not query", async () => {
  const { privateKey: otherKey } = generateKeyPairSync("rsa", { modulusLength: 2048 });
  const cases = [
    "signed-token",
    authorJwt({ exp: Math.floor(Date.now() / 1000) - 60 }),
    authorJwt({ aud: "someone-else" }),
    signJwt(
      { exp: Math.floor(Date.now() / 1000) + 3600, aud: audience },
      { key: otherKey },
    ),
  ];
  for (const assertion of cases) {
    let called = false;
    const previous = globalThis.fetch;
    globalThis.fetch = async () => {
      called = true;
      return sqlResult([]);
    };
    try {
      const response = await worker.fetch(
        new Request("https://counts.revaluator.ai/", {
          headers: { "cf-access-jwt-assertion": assertion },
        }),
        pageEnv(),
      );
      assert.equal(response.status, 403);
      assert.equal(called, false);
    } finally {
      globalThis.fetch = previous;
    }
  }
});

test("a sql failure returns storage error and does not echo the token", async () => {
  const previous = globalThis.fetch;
  globalThis.fetch = async () => new Response("nope sekret", { status: 403 });
  try {
    const response = await worker.fetch(
      new Request("https://counts.revaluator.ai/", {
        headers: { "cf-access-jwt-assertion": authorJwt() },
      }),
      pageEnv(),
    );
    assert.equal(response.status, 500);
    const body = await response.text();
    assert.equal(body, "storage error\n");
    assert.equal(body.includes("sekret"), false);
  } finally {
    globalThis.fetch = previous;
  }
});

function viewBox(html) {
  const match = html.match(/viewBox="([^"]+)"/);
  const [x, y, w, h] = match[1].split(" ").map(Number);
  return { x, y, w, h };
}

function inside(box, lat, lon) {
  const [x, y] = project(lat, lon);
  return x >= box.x && x <= box.x + box.w && y >= box.y && y <= box.y + box.h;
}

test("the map frames every located city and leaves a blank place off it", () => {
  const html = render(report);
  const box = viewBox(html);
  assert.equal(html.includes('class="heat"'), true);
  assert.equal((html.match(/class="heat"/g) || []).length, 1);
  assert.equal(inside(box, 48.85, 2.35), true);
  assert.equal(box.w < 250, true);
  assert.equal(box.w > 40, true);
  assert.equal(html.includes("No city stored yet"), false);
});

test("a spread of cities stays inside one frame", () => {
  const points = [
    { lat: 48.85, lon: 2.35, installs: 2 },
    { lat: 60.17, lon: 24.94, installs: 1 },
    { lat: 28.46, lon: -16.25, installs: 4 },
  ];
  const box = frame(points);
  for (const point of points) assert.equal(inside(box, point.lat, point.lon), true);
  assert.equal(box.w < 1000, true);
});

test("cities on opposite sides of the globe still fit", () => {
  const points = [
    { lat: 48.85, lon: 2.35, installs: 1 },
    { lat: 35.68, lon: 139.69, installs: 1 },
  ];
  const box = frame(points);
  for (const point of points) assert.equal(inside(box, point.lat, point.lon), true);
});

test("locate uses the city directory and not a stored coordinate", () => {
  const paris = locate("Paris", "FR");
  assert.equal(Math.abs(paris.lat - 48.85) < 0.2, true);
  assert.equal(Math.abs(paris.lon - 2.35) < 0.2, true);
  assert.equal(locate("", ""), null);
  assert.equal(locate("<Utrecht>", "NL"), null);
  assert.equal(locate("", "FR") != null, true);
});

test("loadReport keeps only the count fields it renders", async () => {
  const reportFromSql = await loadReport(async (_url, init) => {
    const sql = String(init.body);
    if (sql.includes("toDate")) return sqlResult([{ day: "2026-10-01", installs: "1", index1: "deadbeef" }]);
    if (sql.includes("blob6")) return sqlResult([{ country: "FR", city: "Paris", installs: "1" }]);
    return sqlResult([{ installs: "1" }]);
  }, "sekret");
  assert.equal(render(reportFromSql).includes("deadbeef"), false);
});
