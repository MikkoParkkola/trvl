import { locate } from "./geo.js";
import { mapSvg } from "./map.js";

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

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

function dayText(iso) {
  const [year, month, day] = String(iso).split("-");
  const index = Number(month) - 1;
  if (!year || !day || index < 0 || index > 11) return String(iso);
  return `${Number(day)} ${MONTHS[index]}`;
}

export function render(report) {
  const places = report.places.map((row) => {
    const installs = Number(row.installs) || 0;
    const spot = locate(row.city, row.country);
    return { label: placeLabel(row), installs, spot, named: Boolean(row.city || row.country) };
  });
  const mapped = places.filter((place) => place.spot).map((place) => ({
    label: place.label,
    installs: place.installs,
    lat: place.spot.lat,
    lon: place.spot.lon,
  }));
  const maxPlace = Math.max(1, ...places.map((place) => place.installs));
  const placeRows = places
    .map((place) => {
      const width = Math.max(4, Math.round((100 * place.installs) / maxPlace));
      const missed = place.named && !place.spot ? " not on the map" : "";
      return `<div class="place"><span class="name">${escapeHtml(place.label)}</span><span class="track"><span class="fill" style="width:${width}%"></span></span><span class="n">${escapeHtml(place.installs)}</span>${missed ? `<span class="missed">${missed}</span>` : ""}</div>`;
    })
    .join("");
  const maxDay = Math.max(1, ...report.days.map((row) => Number(row.installs) || 0));
  const dayCols = report.days
    .map((row) => {
      const installs = Number(row.installs) || 0;
      const height = Math.max(8, Math.round((100 * installs) / maxDay));
      return `<div class="col"><div class="plot"><span class="stem" style="height:${height}%"></span></div><span class="n">${escapeHtml(installs)}</span><time datetime="${escapeHtml(row.day)}">${escapeHtml(dayText(row.day))}</time></div>`;
    })
    .join("");
  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>trvl installs</title>
<style>
  :root { color-scheme: light; }
  body { margin: 0; background: #f3efe6; color: #241c16; font: 17px/1.45 Palatino, "Iowan Old Style", Georgia, serif; }
  main { max-width: 52rem; margin: 0 auto; padding: 2rem 1.25rem 3rem; }
  .mark { margin: 0; letter-spacing: 0.16em; text-transform: uppercase; font: 600 0.72rem/1 system-ui, sans-serif; color: #8a5a32; }
  h1 { margin: 0.2rem 0 0; font-size: 2.4rem; font-weight: 600; letter-spacing: -0.03em; }
  .total { margin: 0.35rem 0 1.4rem; color: #5c534b; }
  h2 { margin: 1.6rem 0 0.6rem; font: 600 0.78rem/1 system-ui, sans-serif; letter-spacing: 0.08em; text-transform: uppercase; color: #6f655c; }
  figure { margin: 0; }
  svg { width: 100%; height: auto; display: block; border-radius: 18px; background: #d5e3ec; }
  .sea { fill: #d5e3ec; }
  .land { fill: #f7f3ea; stroke: #cabbab; stroke-width: 1; vector-effect: non-scaling-stroke; }
  .heat { fill: url(#heat); }
  .label { font-family: system-ui, sans-serif; fill: #241c16; text-anchor: middle; stroke: #f7f3ea; stroke-width: 0.35; paint-order: stroke; }
  .empty { font-family: system-ui, sans-serif; fill: #5c534b; text-anchor: middle; }
  .scale { display: flex; align-items: center; gap: 0.6rem; margin-top: 0.55rem; color: #6f655c; font: 0.78rem/1 system-ui, sans-serif; }
  .scale i { flex: 1; height: 0.45rem; border-radius: 99px; background: linear-gradient(90deg, rgba(253,186,116,0.35), #c2410c); }
  .days { display: flex; align-items: stretch; justify-content: flex-start; gap: 0.7rem; height: 9.5rem; overflow-x: auto; padding-bottom: 0.2rem; }
  .col { flex: 0 0 3rem; display: flex; flex-direction: column; align-items: center; gap: 0.2rem; }
  .plot { flex: 1; display: flex; align-items: flex-end; width: 1.15rem; }
  .stem { display: block; width: 100%; background: #245c45; border-radius: 4px 4px 0 0; }
  .n, time { font: 0.75rem/1.2 system-ui, sans-serif; font-variant-numeric: tabular-nums; }
  time { color: #6f655c; }
  .place { display: grid; grid-template-columns: minmax(7rem, 1.1fr) 2fr auto; gap: 0.35rem 0.7rem; align-items: center; padding: 0.35rem 0; border-bottom: 1px solid #e4d9c8; }
  .name { font-family: system-ui, sans-serif; }
  .track { height: 0.45rem; background: #e7dfd2; border-radius: 99px; overflow: hidden; }
  .fill { display: block; height: 100%; background: #c2410c; border-radius: 99px; }
  .missed { grid-column: 1 / -1; margin-top: -0.2rem; color: #8a5a32; font: 0.75rem/1 system-ui, sans-serif; }
  p.note { color: #5c534b; font-size: 0.92rem; }
</style>
</head>
<body>
<main>
<p class="mark">trvl</p>
<h1>Installs</h1>
<p class="total">${escapeHtml(report.installs)} installs in the last 90 days.</p>
<h2>Where they are</h2>
<figure>
${mapSvg(mapped)}
<div class="scale"><span>fewer</span><i></i><span>more</span></div>
</figure>
<h2>By day</h2>
<div class="days">${dayCols}</div>
<h2>Places</h2>
<div class="places">${placeRows}</div>
<p class="note">An install is one copy of trvl, not a person. The install id is not shown. A released build sends at most one heartbeat a day. The city is Cloudflare’s lookup of that connection: a VPN, a relay, or a mobile network can place it at the network exit, and some connections have no city. The map marks the directory position of that city name. It is not a GPS point, and the heartbeat does not store coordinates. Heartbeats from before the city was stored stay blank. Rows are kept for 90 days. Receiver proof rows are left out. Coastline from Natural Earth. City positions from GeoNames.</p>
</main>
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
