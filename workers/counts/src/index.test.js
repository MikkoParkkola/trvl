import assert from "node:assert/strict";
import { createSign, generateKeyPairSync } from "node:crypto";
import test from "node:test";
import { locate } from "./geo.js";
import worker, { QUERIES, loadReport, render } from "./index.js";
import { frame, heatRadius, mapSvg, project } from "./map.js";

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
  products: [{ product: "trvl", installs: "4" }],
  days: [{ product: "trvl", day: "2026-10-01", installs: "4" }],
  places: [
    { product: "trvl", country: "FR", city: "Paris", installs: "2" },
    { product: "trvl", country: "", city: "", installs: "2" },
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
    assert.equal(sql.includes("blob3 NOT IN"), true);
    assert.match(sql, /blob1 AS product/);
    assert.match(sql, /GROUP BY[\s\S]*\bproduct\b/);
    assert.equal(sql.includes("blob1 = 'trvl'"), false);
    assert.equal(sql.includes("SELECT index1"), false);
    assert.equal(sql.includes("SELECT blob5"), false);
  }
  assert.equal(QUERIES.byMachine.includes("blob9"), true);
  assert.equal(QUERIES.byInstalled.includes("blob8"), true);
  assert.deepEqual(Object.keys(QUERIES).sort(), [
    "byDay",
    "byInstalled",
    "byMachine",
    "byPlace",
    "byProduct",
  ]);
});

test("the page splits installs by product and does not add them together", () => {
  const html = render({
    products: [
      { product: "trvl", installs: "4" },
      { product: "nab", installs: "2" },
      { product: "<mcp-gateway>", installs: "1" },
    ],
    days: [
      { product: "trvl", day: "2026-10-01", installs: "4" },
      { product: "nab", day: "2026-10-02", installs: "2" },
    ],
    places: [
      { product: "trvl", country: "FR", city: "Paris", installs: "4" },
      { product: "nab", country: "NL", city: "Amsterdam", installs: "2" },
    ],
  });
  const trvl = html.split('data-product="nab"')[0];
  const nab = html.split('data-product="nab"')[1].split('data-product="')[0];
  assert.match(trvl, /data-product="trvl"/);
  assert.match(trvl, /4 installs in the last 90 days/);
  assert.match(trvl, /Paris, FR/);
  assert.match(trvl, /2026-10-01/);
  assert.equal(trvl.includes("Amsterdam"), false);
  assert.equal(trvl.includes("2026-10-02"), false);
  assert.match(nab, /2 installs in the last 90 days/);
  assert.match(nab, /Amsterdam, NL/);
  assert.match(nab, /2026-10-02/);
  assert.equal(nab.includes("Paris"), false);
  assert.equal(html.includes("6 installs"), false);
  assert.equal(html.includes("7 installs"), false);
  assert.match(html, /&lt;mcp-gateway&gt;/);
  assert.equal(html.includes("<mcp-gateway>"), false);
});

test("cohorts stay inside one product and a shared machine is counted without its id", () => {
  const machine = "0123456789abcdef0123456789abcdef";
  const other = "fedcba9876543210fedcba9876543210";
  const html = render({
    today: "2026-10-02",
    products: [
      { product: "trvl", installs: "4" },
      { product: "nab", installs: "2" },
    ],
    days: [],
    places: [],
    installed: [
      { product: "trvl", installed: "2026-09-01", installs: "3" },
      { product: "trvl", installed: "2026-10-01", installs: "1" },
      { product: "nab", installed: "2025-01-01", installs: "2" },
    ],
    machines: [
      { product: "trvl", machine, installs: "4" },
      { product: "nab", machine, installs: "2" },
      { product: "nab", machine: other, installs: "1" },
    ],
  });
  const trvl = html.split('data-product="nab"')[0];
  const nab = html.split('data-product="nab"')[1];
  assert.match(trvl, /Using it for 30 to 89 days: 3/);
  assert.match(trvl, /Using it for under 7 days: 1/);
  assert.match(trvl, /Installed 2026-09: 3/);
  assert.equal(trvl.includes("a year or more"), false);
  assert.match(nab, /Using it for a year or more: 2/);
  assert.equal(nab.includes("30 to 89 days"), false);
  assert.match(html, /1 machine runs nab and trvl/);
  assert.equal(html.includes(machine), false);
  assert.equal(html.includes(other), false);
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
    return sqlResult([{ product: "trvl", installs: "4", index1: "deadbeef" }]);
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
    assert.equal(seen.length, 5);
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

test("one or two installs on a world view stay a city mark", () => {
  const svg = mapSvg([
    { label: "Wheaton, US", installs: 1, lat: 41.86, lon: -88.11 },
    { label: "Paris, FR", installs: 1, lat: 48.85, lon: 2.35 },
    { label: "Shanghai, CN", installs: 2, lat: 31.22, lon: 121.47 },
  ]);
  const box = viewBox(svg);
  assert.equal(box.w > 900, true);
  const radii = [...svg.matchAll(/\sr="([0-9.]+)"/g)].map((match) => Number(match[1]));
  assert.equal(radii.length, 3);
  for (const radius of radii) {
    assert.equal(radius > 2, true);
    assert.equal(radius < 22, true);
    assert.equal(radius / box.w < 0.025, true);
  }
  assert.equal(heatRadius(1, 2, 1000) < heatRadius(2, 2, 1000), true);
  assert.equal(heatRadius(2, 2, 1000) < 16, true);
  assert.equal(svg.includes("feGaussianBlur"), false);
  assert.equal((svg.match(/class="heat"/g) || []).length, 3);
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
    if (sql.includes("toDate")) {
      return sqlResult([{ product: "trvl", day: "2026-10-01", installs: "1", index1: "deadbeef" }]);
    }
    if (sql.includes("blob6")) return sqlResult([{ product: "trvl", country: "FR", city: "Paris", installs: "1" }]);
    return sqlResult([{ product: "trvl", installs: "1", blob5: "deadbeef" }]);
  }, "sekret");
  assert.equal(render(reportFromSql).includes("deadbeef"), false);
});
