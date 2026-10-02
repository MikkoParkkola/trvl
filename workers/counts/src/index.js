import { locate } from "./geo.js";
import { mapSvg } from "./map.js";

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
  const [products, days, places, installed, machines] = await Promise.all([
    query(fetchImpl, token, QUERIES.byProduct),
    query(fetchImpl, token, QUERIES.byDay),
    query(fetchImpl, token, QUERIES.byPlace),
    query(fetchImpl, token, QUERIES.byInstalled),
    query(fetchImpl, token, QUERIES.byMachine),
  ]);
  return { products, days, places, installed, machines };
}

function productName(row) {
  if (!row || row.product == null) return "";
  return String(row.product);
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

function sectionOrder(report) {
  const names = [];
  const seen = new Set();
  const add = (name) => {
    if (seen.has(name)) return;
    seen.add(name);
    names.push(name);
  };
  for (const row of report.products || []) add(productName(row));
  for (const row of report.days || []) add(productName(row));
  for (const row of report.places || []) add(productName(row));
  return names;
}

function productTotal(report, name) {
  for (const row of report.products || []) {
    if (productName(row) === name) return row.installs ?? "0";
  }
  return null;
}

function daysBetween(start, end) {
  const from = Date.parse(`${start}T00:00:00Z`);
  const to = Date.parse(`${end}T00:00:00Z`);
  if (Number.isNaN(from) || Number.isNaN(to)) return null;
  return Math.round((to - from) / 86400000);
}

function tenureHtml(report, name) {
  const rows = (report.installed || []).filter((row) => productName(row) === name);
  if (rows.length === 0) return "";
  const today = /^\d{4}-\d{2}-\d{2}$/.test(String(report.today || ""))
    ? String(report.today)
    : new Date().toISOString().slice(0, 10);
  const buckets = [
    ["under 7 days", 0, 6],
    ["7 to 29 days", 7, 29],
    ["30 to 89 days", 30, 89],
    ["90 to 364 days", 90, 364],
    ["a year or more", 365, 100000],
  ];
  const counts = buckets.map(() => 0);
  let unknown = 0;
  const months = new Map();
  for (const row of rows) {
    const installs = Number(row.installs) || 0;
    const day = row.installed ? String(row.installed) : "";
    const age = /^\d{4}-\d{2}-\d{2}$/.test(day) ? daysBetween(day, today) : null;
    if (age == null || age < 0) {
      unknown += installs;
      continue;
    }
    const index = buckets.findIndex((bucket) => age >= bucket[1] && age <= bucket[2]);
    if (index >= 0) counts[index] += installs;
    const month = day.slice(0, 7);
    months.set(month, (months.get(month) || 0) + installs);
  }
  const parts = [];
  buckets.forEach((bucket, index) => {
    if (counts[index] > 0) parts.push(`Using it for ${bucket[0]}: ${counts[index]}.`);
  });
  if (unknown > 0) parts.push(`Install date not sent yet: ${unknown}.`);
  for (const [month, installs] of [...months.entries()].sort()) {
    parts.push(`Installed ${month}: ${installs}.`);
  }
  if (parts.length === 0) return "";
  const items = parts.map((part) => `<li>${escapeHtml(part.replace(/\.$/, ""))}</li>`).join("");
  return `<h2>How long</h2><ul class="tenure">${items}</ul>`;
}

function overlapHtml(report) {
  const grouped = new Map();
  for (const row of report.machines || []) {
    const machine = row.machine ? String(row.machine) : "";
    const name = productName(row);
    if (!machine || !name) continue;
    if (!grouped.has(machine)) grouped.set(machine, new Set());
    grouped.get(machine).add(name);
  }
  if (grouped.size === 0) {
    return `<section class="overlap"><h2>Same machine</h2><p class="total">No heartbeat has included a machine id yet, so products cannot be matched.</p></section>`;
  }
  const combos = new Map();
  for (const names of grouped.values()) {
    if (names.size < 2) continue;
    const label = [...names].sort().join(" and ");
    combos.set(label, (combos.get(label) || 0) + 1);
  }
  if (combos.size === 0) {
    return `<section class="overlap"><h2>Same machine</h2><p class="total">No machine has reported more than one product.</p></section>`;
  }
  const lines = [...combos.entries()]
    .sort((left, right) => right[1] - left[1] || left[0].localeCompare(right[0]))
    .map(([label, count]) => {
      const verb = count === 1 ? "machine runs" : "machines run";
      return `<li>${escapeHtml(count)} ${verb} ${escapeHtml(label)}.</li>`;
    })
    .join("");
  return `<section class="overlap"><h2>Same machine</h2><ul>${lines}</ul></section>`;
}

function productBlock(report, name, index) {
  const places = (report.places || [])
    .filter((row) => productName(row) === name)
    .map((row) => {
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
  const days = (report.days || []).filter((row) => productName(row) === name);
  const maxDay = Math.max(1, ...days.map((row) => Number(row.installs) || 0));
  const dayCols = days
    .map((row) => {
      const installs = Number(row.installs) || 0;
      const height = Math.max(8, Math.round((100 * installs) / maxDay));
      return `<div class="col"><div class="plot"><span class="stem" style="height:${height}%"></span></div><span class="n">${escapeHtml(installs)}</span><time datetime="${escapeHtml(row.day)}">${escapeHtml(dayText(row.day))}</time></div>`;
    })
    .join("");
  const total = productTotal(report, name);
  const totalLine = total == null ? "" : `<p class="total">${escapeHtml(total)} installs in the last 90 days.</p>`;
  const heading = name === "" ? "unnamed" : name;
  const dayBlock = dayCols ? `<h2>By day</h2><div class="days">${dayCols}</div>` : "";
  const placeBlock = placeRows ? `<h2>Places</h2><div class="places">${placeRows}</div>` : "";
  return `<section class="product" data-product="${escapeHtml(name)}">
<h2 class="product">${escapeHtml(heading)}</h2>
${totalLine}
<h2>Where they are</h2>
<figure class="atlas-wrap">
${mapSvg(mapped, `heat-${index}`)}
<figcaption>
<span class="hint">Drag to move. Scroll or pinch to zoom.</span>
<div class="tools">
<button type="button" data-zoom="in" aria-label="Zoom in">+</button>
<button type="button" data-zoom="out" aria-label="Zoom out">−</button>
<button type="button" data-zoom="home" aria-label="Fit the cities">Fit</button>
</div>
<span class="scale"><span>fewer</span><i></i><span>more</span></span>
</figcaption>
</figure>
${tenureHtml(report, name)}
${dayBlock}
${placeBlock}
</section>`;
}

export function render(report) {
  const blocks = sectionOrder(report)
    .map((name, index) => productBlock(report, name, index))
    .join("");
  const overlap = overlapHtml(report);
  const empty = blocks ? "" : `<p class="total">No heartbeats in the last 90 days.</p>`;
  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Installs</title>
<style>
  :root {
    color-scheme: light;
    --paper: #f3efe6;
    --ink: #241c16;
    --muted: #5c534b;
    --faint: #6f655c;
    --line: #e4d9c8;
    --sea: #d5e3ec;
    --land: #f7f3ea;
    --heat: #9a3412;
    --card: #efe6d8;
  }
  body { margin: 0; background: var(--paper); color: var(--ink); font: 17px/1.45 Palatino, "Iowan Old Style", Georgia, serif; }
  main { max-width: 68rem; margin: 0 auto; padding: 2.4rem 1.25rem 4rem; }
  .mark { margin: 0; letter-spacing: 0.18em; text-transform: uppercase; font: 600 0.72rem/1 system-ui, sans-serif; color: #8a5a32; }
  h1 { margin: 0.25rem 0 0; font-size: clamp(2.4rem, 5vw, 3.4rem); font-weight: 600; letter-spacing: -0.035em; line-height: 0.95; }
  .lede { margin: 0.55rem 0 0; max-width: 38rem; color: var(--muted); }
  section.overlap { margin-top: 1.7rem; background: var(--card); border-radius: 16px; padding: 0.95rem 1.15rem 1.05rem; }
  section.overlap ul { margin: 0.15rem 0 0; padding-left: 1.15rem; }
  section.overlap .total { margin: 0; }
  section.product { display: grid; grid-template-columns: 1fr auto; align-items: baseline; column-gap: 1.25rem; margin-top: 2.4rem; }
  section.product + section.product { border-top: 1px solid var(--line); padding-top: 0.4rem; }
  h2.product { grid-column: 1; margin: 0; font: 600 2rem/1.05 Palatino, "Iowan Old Style", Georgia, serif; letter-spacing: -0.03em; text-transform: none; color: var(--ink); }
  section.product > .total { grid-column: 2; margin: 0; text-align: right; color: var(--muted); font-variant-numeric: tabular-nums; }
  section.product > h2, section.product > figure, section.product > ul, section.product > div { grid-column: 1 / -1; }
  h2 { margin: 1.45rem 0 0.55rem; font: 600 0.72rem/1 system-ui, sans-serif; letter-spacing: 0.12em; text-transform: uppercase; color: var(--faint); }
  section.overlap h2 { margin: 0 0 0.35rem; }
  .tenure { display: flex; flex-wrap: wrap; gap: 0.4rem; margin: 0; padding: 0; list-style: none; }
  .tenure li { font: 0.82rem/1.35 system-ui, sans-serif; background: var(--card); color: #3a3128; border-radius: 999px; padding: 0.32rem 0.75rem; }
  .atlas-wrap { margin: 0.15rem 0 0; }
  .atlas-wrap:has(svg:focus-visible) { border-radius: 18px; box-shadow: 0 0 0 2px #8a5a32; }
  .tools { display: flex; gap: 0.1rem; padding: 0.12rem; border-radius: 999px; background: var(--card); }
  .tools button { min-width: 2.5rem; height: 2.25rem; padding: 0 0.7rem; border: 0; border-radius: 999px; background: transparent; color: var(--ink); font: 600 0.92rem/1 system-ui, sans-serif; cursor: pointer; }
  .tools button:hover { background: #fff; }
  svg.atlas { width: 100%; height: auto; display: block; border-radius: 18px; background: var(--sea); cursor: grab; touch-action: none; outline: none; overflow: hidden; box-shadow: 0 1px 0 rgba(36, 28, 22, 0.04), 0 16px 40px rgba(36, 28, 22, 0.06); user-select: none; }
  svg.atlas.dragging { cursor: grabbing; }
  .sea { fill: var(--sea); }
  .land { fill: var(--land); stroke: #cabbab; stroke-width: 1; vector-effect: non-scaling-stroke; }
  .label { font-family: system-ui, sans-serif; font-weight: 560; fill: var(--ink); text-anchor: middle; stroke: var(--land); stroke-width: 0.4em; paint-order: stroke; }
  .empty { font-family: system-ui, sans-serif; fill: var(--muted); text-anchor: middle; }
  figcaption { display: flex; flex-wrap: wrap; gap: 0.45rem 0.75rem; align-items: center; margin-top: 0.6rem; color: var(--faint); font: 0.78rem/1.3 system-ui, sans-serif; }
  figcaption .hint { margin-right: auto; }
  .scale { display: flex; align-items: center; gap: 0.45rem; }
  .scale i { width: 5.5rem; height: 0.42rem; border-radius: 99px; background: linear-gradient(90deg, rgba(253, 186, 116, 0.45), var(--heat)); }
  .days { display: flex; align-items: stretch; justify-content: flex-start; gap: 0.7rem; height: 9rem; overflow-x: auto; padding-bottom: 0.2rem; }
  .col { flex: 0 0 3rem; display: flex; flex-direction: column; align-items: center; gap: 0.2rem; }
  .plot { flex: 1; display: flex; align-items: flex-end; width: 1.2rem; }
  .stem { display: block; width: 100%; background: var(--heat); border-radius: 4px 4px 0 0; }
  .n, time { font: 0.75rem/1.2 system-ui, sans-serif; font-variant-numeric: tabular-nums; }
  time { color: var(--faint); }
  .place { display: grid; grid-template-columns: minmax(7rem, 1.1fr) 2fr auto; gap: 0.35rem 0.75rem; align-items: center; padding: 0.42rem 0; border-bottom: 1px solid var(--line); }
  .name { font-family: system-ui, sans-serif; }
  .track { height: 0.42rem; background: #e7dfd2; border-radius: 99px; overflow: hidden; }
  .fill { display: block; height: 100%; background: var(--heat); border-radius: 99px; }
  .missed { grid-column: 1 / -1; margin-top: -0.15rem; color: #8a5a32; font: 0.75rem/1 system-ui, sans-serif; }
  p.note { margin-top: 2.4rem; padding-top: 1.1rem; border-top: 1px solid var(--line); color: var(--muted); font-size: 0.88rem; }
  @media (max-width: 640px) {
    section.product > .total { grid-column: 1; text-align: left; margin-top: 0.25rem; }
    h2.product { font-size: 1.7rem; }
  }
</style>
</head>
<body>
<main>
<p class="mark">telemetry</p>
<h1>Installs</h1>
<p class="lede">Each product on its own, for the last 90 days. A mark on the map is the city, not the country around it.</p>
${overlap}
${empty}${blocks}
<p class="note">An install is one copy of one product, not a person. Each product is counted on its own. The install id is not shown. A released build sends at most one heartbeat a day. install_date is the UTC day that copy first stored its install id, sent again later so the cohort is still known after older rows age out. The machine id is a random value shared by the products on one machine. It matches those products to the same machine. It is not a name, and it is not shown. The city is Cloudflare’s lookup of that connection: a VPN, a relay, or a mobile network can place it at the network exit, and some connections have no city. The map marks the directory position of that city name. It is not a GPS point, and the heartbeat does not store coordinates. Heartbeats from before the city was stored stay blank. Rows are kept for 90 days. Receiver proof rows are left out. Coastline from Natural Earth. City positions from GeoNames.</p>
</main>
<script>
(function () {
  function nums(value) {
    return String(value || "").trim().split(" ").map(Number);
  }
  function boxOf(parts) {
    return { x: parts[0], y: parts[1], w: parts[2], h: parts[3] };
  }
  function apply(svg, view) {
    svg.setAttribute("viewBox", [view.x, view.y, view.w, view.h].map(function (n) { return n.toFixed(2); }).join(" "));
    var size = view.w * 0.018;
    var lift = view.w * 0.028;
    svg.querySelectorAll(".label").forEach(function (text) {
      text.setAttribute("font-size", size.toFixed(2));
      var y = Number(text.getAttribute("data-py"));
      var x = Number(text.getAttribute("data-px"));
      if (y !== y) return;
      var above = y - lift;
      text.setAttribute("y", (above < view.y + lift ? y + lift * 1.15 : above).toFixed(2));
      if (x !== x || typeof text.getComputedTextLength !== "function") return;
      var half = text.getComputedTextLength() / 2;
      if (!(half > 0)) return;
      var lx = x;
      if (x - half < view.x) lx = view.x + half;
      if (lx + half > view.x + view.w) lx = view.x + view.w - half;
      text.setAttribute("x", lx.toFixed(2));
    });
    svg.querySelectorAll(".heat").forEach(function (circle) {
      var span = Number(circle.getAttribute("data-span"));
      if (span === span && span > 0) circle.setAttribute("r", (view.w * span).toFixed(2));
    });
  }
  function clamp(view, world) {
    var w = Math.min(Math.max(view.w, world.w * 0.025), world.w);
    var h = w * (world.h / world.w);
    if (h > world.h) {
      h = world.h;
      w = h * (world.w / world.h);
    }
    var x = Math.min(Math.max(view.x, world.x), world.x + world.w - w);
    var y = Math.min(Math.max(view.y, world.y), world.y + world.h - h);
    return { x: x, y: y, w: w, h: h };
  }
  document.querySelectorAll("svg.atlas").forEach(function (svg) {
    var world = boxOf(nums(svg.getAttribute("data-world")));
    var home = boxOf(nums(svg.getAttribute("data-home")));
    if (!world.w || !home.w) return;
    function current() {
      return boxOf(nums(svg.getAttribute("viewBox")));
    }
    function setView(next) {
      apply(svg, clamp(next, world));
    }
    function zoom(factor, ux, uy) {
      var view = current();
      var w = view.w * factor;
      var h = view.h * factor;
      setView({
        x: ux - (ux - view.x) * (w / view.w),
        y: uy - (uy - view.y) * (h / view.h),
        w: w,
        h: h,
      });
    }
    function focusAt() {
      var view = current();
      var marks = [];
      svg.querySelectorAll(".heat").forEach(function (circle) {
        var x = Number(circle.getAttribute("cx"));
        var y = Number(circle.getAttribute("cy"));
        if (x >= view.x && x <= view.x + view.w && y >= view.y && y <= view.y + view.h) marks.push([x, y]);
      });
      if (marks.length === 0) return [view.x + view.w / 2, view.y + view.h / 2];
      var sx = 0;
      var sy = 0;
      marks.forEach(function (point) {
        sx += point[0];
        sy += point[1];
      });
      return [sx / marks.length, sy / marks.length];
    }
    function userAt(clientX, clientY) {
      var rect = svg.getBoundingClientRect();
      var view = current();
      return [
        view.x + ((clientX - rect.left) / rect.width) * view.w,
        view.y + ((clientY - rect.top) / rect.height) * view.h,
      ];
    }
    svg.addEventListener("wheel", function (event) {
      event.preventDefault();
      var delta = event.deltaY;
      if (event.deltaMode === 1) delta *= 16;
      else if (event.deltaMode === 2) delta *= 400;
      var at = userAt(event.clientX, event.clientY);
      zoom(Math.exp(delta * 0.0012), at[0], at[1]);
    }, { passive: false });
    var drag = null;
    var pinch = null;
    var points = new Map();
    function livePoints() {
      return Array.from(points.values());
    }
    function pinchStart() {
      var both = livePoints();
      var dist = Math.hypot(both[0].x - both[1].x, both[0].y - both[1].y);
      var rect = svg.getBoundingClientRect();
      var view = current();
      var cx = (both[0].x + both[1].x) / 2;
      var cy = (both[0].y + both[1].y) / 2;
      pinch = {
        dist: dist,
        view: view,
        ux: view.x + ((cx - rect.left) / rect.width) * view.w,
        uy: view.y + ((cy - rect.top) / rect.height) * view.h,
      };
      drag = null;
      svg.classList.remove("dragging");
    }
    svg.addEventListener("pointerdown", function (event) {
      if (event.button !== 0) return;
      points.set(event.pointerId, { x: event.clientX, y: event.clientY });
      svg.setPointerCapture(event.pointerId);
      if (points.size >= 2) {
        pinchStart();
        return;
      }
      var view = current();
      drag = { id: event.pointerId, x: event.clientX, y: event.clientY, vx: view.x, vy: view.y, w: view.w, h: view.h };
      svg.classList.add("dragging");
    });
    svg.addEventListener("pointermove", function (event) {
      if (!points.has(event.pointerId)) return;
      points.set(event.pointerId, { x: event.clientX, y: event.clientY });
      if (points.size >= 2 && pinch && pinch.dist > 0) {
        var both = livePoints();
        var dist = Math.hypot(both[0].x - both[1].x, both[0].y - both[1].y);
        if (dist <= 0) return;
        var factor = pinch.dist / dist;
        var rect = svg.getBoundingClientRect();
        var cx = (both[0].x + both[1].x) / 2;
        var cy = (both[0].y + both[1].y) / 2;
        var w = pinch.view.w * factor;
        var h = pinch.view.h * factor;
        setView({
          x: pinch.ux - ((cx - rect.left) / rect.width) * w,
          y: pinch.uy - ((cy - rect.top) / rect.height) * h,
          w: w,
          h: h,
        });
        return;
      }
      if (!drag || event.pointerId !== drag.id) return;
      var rect = svg.getBoundingClientRect();
      var dx = ((event.clientX - drag.x) / rect.width) * drag.w;
      var dy = ((event.clientY - drag.y) / rect.height) * drag.h;
      setView({ x: drag.vx - dx, y: drag.vy - dy, w: drag.w, h: drag.h });
    });
    function endDrag(event) {
      points.delete(event.pointerId);
      if (drag && event.pointerId === drag.id) drag = null;
      if (points.size < 2) pinch = null;
      if (points.size === 0) svg.classList.remove("dragging");
    }
    svg.addEventListener("pointerup", endDrag);
    svg.addEventListener("pointercancel", endDrag);
    svg.addEventListener("dblclick", function (event) {
      var at = userAt(event.clientX, event.clientY);
      zoom(0.55, at[0], at[1]);
    });
    svg.addEventListener("keydown", function (event) {
      var view = current();
      var step = view.w * 0.12;
      var at = focusAt();
      if (event.key === "ArrowLeft") setView({ x: view.x - step, y: view.y, w: view.w, h: view.h });
      else if (event.key === "ArrowRight") setView({ x: view.x + step, y: view.y, w: view.w, h: view.h });
      else if (event.key === "ArrowUp") setView({ x: view.x, y: view.y - step, w: view.w, h: view.h });
      else if (event.key === "ArrowDown") setView({ x: view.x, y: view.y + step, w: view.w, h: view.h });
      else if (event.key === "+" || event.key === "=") zoom(0.75, at[0], at[1]);
      else if (event.key === "-" || event.key === "_") zoom(1.33, at[0], at[1]);
      else if (event.key === "0") setView(home);
      else return;
      event.preventDefault();
    });
    apply(svg, current());
    var wrap = svg.closest(".atlas-wrap");
    if (!wrap) return;
    wrap.querySelectorAll("button[data-zoom]").forEach(function (button) {
      button.addEventListener("click", function () {
        var which = button.getAttribute("data-zoom");
        var at = focusAt();
        if (which === "in") zoom(0.7, at[0], at[1]);
        else if (which === "out") zoom(1.4, at[0], at[1]);
        else setView(home);
      });
    });
  });
})();
</script>
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
