// Private install counts for the trvl author. Cloudflare Access sits in front
// of counts.revaluator.ai. This worker still refuses a request that did not
// come through that login. It reads Analytics Engine over the SQL API and
// renders counts only. Install ids are never selected and never written out.

const ACCOUNT = "e88256e8f368cdae614ec878b7b8f98a";
const SQL_URL = `https://api.cloudflare.com/client/v4/accounts/${ACCOUNT}/analytics_engine/sql`;
const ACCESS_CERTS = "https://revaluator.cloudflareaccess.com/cdn-cgi/access/certs";

const COUNTED = `
WHERE timestamp > NOW() - INTERVAL '90' DAY
  AND index1 != 'probe-city-20261001'
  AND blob3 NOT IN ('probe', 'go-http')`;

export const QUERIES = {
  total: `SELECT count(DISTINCT index1) AS installs FROM trvl_heartbeat ${COUNTED}`,
  byDay: `SELECT toDate(timestamp) AS day, count(DISTINCT index1) AS installs
FROM trvl_heartbeat ${COUNTED}
GROUP BY day ORDER BY day`,
  byPlace: `SELECT blob7 AS country, blob6 AS city, count(DISTINCT index1) AS installs
FROM trvl_heartbeat ${COUNTED}
GROUP BY country, city ORDER BY installs DESC`,
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

function escapeHtml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
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
  const [totalRows, days, places] = await Promise.all([
    query(fetchImpl, token, QUERIES.total),
    query(fetchImpl, token, QUERIES.byDay),
    query(fetchImpl, token, QUERIES.byPlace),
  ]);
  return {
    installs: totalRows[0]?.installs ?? "0",
    days,
    places,
  };
}

function placeLabel(row) {
  const city = row.city ? String(row.city) : "";
  const country = row.country ? String(row.country) : "";
  if (!city && !country) return "no city stored";
  if (!city) return country;
  if (!country) return city;
  return `${city}, ${country}`;
}

export function render(report) {
  const dayRows = report.days
    .map(
      (row) =>
        `<tr><td>${escapeHtml(row.day)}</td><td>${escapeHtml(row.installs)}</td></tr>`,
    )
    .join("");
  const placeRows = report.places
    .map(
      (row) =>
        `<tr><td>${escapeHtml(placeLabel(row))}</td><td>${escapeHtml(row.installs)}</td></tr>`,
    )
    .join("");
  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>trvl installs</title>
<style>
  body { font: 16px/1.45 system-ui, sans-serif; margin: 2rem auto; max-width: 36rem; color: #1a1a1a; }
  h1 { font-size: 1.4rem; font-weight: 650; }
  table { border-collapse: collapse; width: 100%; margin: 0.5rem 0 1.5rem; }
  th, td { text-align: left; padding: 0.3rem 0.6rem 0.3rem 0; border-bottom: 1px solid #ddd; }
  th { font-weight: 650; }
  p.note { color: #333; }
</style>
</head>
<body>
<h1>trvl installs</h1>
<p>${escapeHtml(report.installs)} installs in the last 90 days.</p>
<h2>By day</h2>
<table>
<thead><tr><th>Day (UTC)</th><th>Installs</th></tr></thead>
<tbody>${dayRows}</tbody>
</table>
<h2>Where they are</h2>
<table>
<thead><tr><th>Place</th><th>Installs</th></tr></thead>
<tbody>${placeRows}</tbody>
</table>
<p class="note">An install is one copy of trvl, not a person. The install id is not shown. A released build sends at most one heartbeat a day. The city is Cloudflare’s lookup of that connection: a VPN, a relay, or a mobile network can place it at the network exit, and some connections have no city. Heartbeats from before the city was stored stay blank. Rows are kept for 90 days. Receiver proof rows are left out.</p>
</body>
</html>`;
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
