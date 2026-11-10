import { DASHBOARD_QUERIES, renderDashboard, shape } from "./dashboard.js";

// Private install counts for the trvl author. Cloudflare Access sits in front
// of counts.revaluator.ai. This worker still refuses a request that did not
// come through that login. It reads Analytics Engine over the SQL API and
// renders counts only. Install ids are never selected and never written out.

const ACCOUNT = "e88256e8f368cdae614ec878b7b8f98a";
const SQL_URL = `https://api.cloudflare.com/client/v4/accounts/${ACCOUNT}/analytics_engine/sql`;
const ACCESS_CERTS = "https://revaluator.cloudflareaccess.com/cdn-cgi/access/certs";

// blob1 is the heartbeat project name. Products share this dataset.
// Every count groups by that name, so one product is not added into another.
const COUNTED = `
WHERE timestamp > NOW() - INTERVAL '90' DAY
  AND index1 != 'probe-city-20261001'
  AND blob3 NOT IN ('probe', 'go-http')`;

export const QUERIES = {
  byProduct: `SELECT blob1 AS product, count(DISTINCT index1) AS installs
FROM trvl_heartbeat ${COUNTED}
GROUP BY product ORDER BY installs DESC`,
  byDay: `SELECT blob1 AS product, toDate(timestamp) AS day, count(DISTINCT index1) AS installs
FROM trvl_heartbeat ${COUNTED}
GROUP BY product, day ORDER BY product, day`,
  byPlace: `SELECT blob1 AS product, blob7 AS country, blob6 AS city, count(DISTINCT index1) AS installs
FROM trvl_heartbeat ${COUNTED}
GROUP BY product, country, city ORDER BY installs DESC`,
  // blob8 is the UTC day the install id was created. It rides on later
  // heartbeats, so a cohort survives after the first row ages out.
  byInstalled: `SELECT blob1 AS product, blob8 AS installed, count(DISTINCT index1) AS installs
FROM trvl_heartbeat ${COUNTED}
GROUP BY product, installed ORDER BY product, installed`,
  // blob9 is read only to match products on one machine. It is not rendered.
  byMachine: `SELECT blob1 AS product, blob9 AS machine, count(DISTINCT index1) AS installs
FROM trvl_heartbeat ${COUNTED}
  AND blob9 != ''
GROUP BY product, machine`,
};

function text(status, body) {
  return new Response(body, {
    status,
    headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" },
  });
}

function bytesToString(bytes) {
  return new TextDecoder().decode(bytes);
}

function base64UrlToBytes(value) {
  const padded = value + "=".repeat((4 - (value.length % 4)) % 4);
  const binary = atob(padded.replaceAll("-", "+").replaceAll("_", "/"));
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

export async function accessAllows(jwt, aud, keys) {
  if (!jwt || !aud) return false;
  const parts = String(jwt).split(".");
  if (parts.length !== 3) return false;
  let header;
  let payload;
  try {
    header = JSON.parse(bytesToString(base64UrlToBytes(parts[0])));
    payload = JSON.parse(bytesToString(base64UrlToBytes(parts[1])));
  } catch {
    return false;
  }
  if (header.alg !== "RS256" || !header.kid) return false;
  const now = Math.floor(Date.now() / 1000);
  if (typeof payload.exp !== "number" || payload.exp <= now) return false;
  const audiences = Array.isArray(payload.aud) ? payload.aud : [payload.aud];
  if (!audiences.includes(aud)) return false;
  const jwk = (keys || []).find((key) => key && key.kid === header.kid);
  if (!jwk) return false;
  const key = await crypto.subtle.importKey(
    "jwk",
    jwk,
    { name: "RSASSA-PKCS1-v1_5", hash: "SHA-256" },
    false,
    ["verify"],
  );
  const signed = new TextEncoder().encode(`${parts[0]}.${parts[1]}`);
  return crypto.subtle.verify("RSASSA-PKCS1-v1_5", key, base64UrlToBytes(parts[2]), signed);
}

async function accessKeys(env) {
  if (env.TEST_ACCESS_KEYS) return JSON.parse(env.TEST_ACCESS_KEYS);
  const response = await fetch(ACCESS_CERTS);
  if (!response.ok) throw new Error("certs");
  const payload = await response.json();
  if (!Array.isArray(payload.keys)) throw new Error("certs");
  return payload.keys;
}

async function query(fetchImpl, token, sql) {
  const response = await fetchImpl(SQL_URL, {
    method: "POST",
    headers: { authorization: `Bearer ${token}` },
    body: sql,
  });
  if (!response.ok) throw new Error("sql");
  const payload = await response.json();
  const rows = payload.data ?? payload.result?.data;
  if (!Array.isArray(rows)) throw new Error("sql");
  return rows;
}

export async function loadReport(fetchImpl, token) {
  const queries = { ...QUERIES, ...DASHBOARD_QUERIES };
  const entries = await Promise.all(
    Object.entries(queries).map(async ([name, sql]) => [name, await query(fetchImpl, token, sql)]),
  );
  return shape(Object.fromEntries(entries));
}

export function render(input, today) {
  return renderDashboard(input, today);
}

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    if (url.pathname !== "/") return text(404, "not found\n");
    if (request.method !== "GET") return text(405, "method not allowed\n");
    const assertion = request.headers.get("cf-access-jwt-assertion");
    if (!assertion) return text(403, "forbidden\n");
    let keys;
    try {
      keys = await accessKeys(env);
    } catch {
      return text(403, "forbidden\n");
    }
    if (!(await accessAllows(assertion, env.ACCESS_AUD, keys))) return text(403, "forbidden\n");
    if (!env.ANALYTICS_READ_TOKEN) return text(500, "storage error\n");
    try {
      const report = await loadReport(fetch, env.ANALYTICS_READ_TOKEN);
      return new Response(render(report), {
        status: 200,
        headers: {
          "content-type": "text/html; charset=utf-8",
          "cache-control": "no-store",
        },
      });
    } catch {
      return text(500, "storage error\n");
    }
  },
};
