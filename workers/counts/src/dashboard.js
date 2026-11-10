import { locate } from "./geo.js";

const FILTER = `
  AND index1 != 'probe-city-20261001'
  AND blob3 NOT IN ('probe', 'go-http')`;

function windowed(days) {
  return `WHERE timestamp > NOW() - INTERVAL '${days}' DAY
${FILTER}`;
}

const COUNTED = `WHERE timestamp > NOW() - INTERVAL '90' DAY
${FILTER}`;

// index1 is only a grouping key. retention is the query that reads it,
// under the alias install, and shape() drops that field before render.
export const DASHBOARD_QUERIES = {
  window7: `SELECT blob1 AS product, count(DISTINCT index1) AS installs
FROM trvl_heartbeat ${windowed(7)}
GROUP BY product`,
  window30: `SELECT blob1 AS product, count(DISTINCT index1) AS installs
FROM trvl_heartbeat ${windowed(30)}
GROUP BY product`,
  weeks: `SELECT blob1 AS product, toStartOfWeek(timestamp) AS week, count(DISTINCT index1) AS installs
FROM trvl_heartbeat ${COUNTED}
GROUP BY product, week`,
  latestPlace: `SELECT blob1 AS product, argMax(blob6, timestamp) AS city, argMax(blob7, timestamp) AS country
FROM trvl_heartbeat ${COUNTED}
GROUP BY product, index1`,
  latestVersion: `SELECT blob1 AS product, argMax(blob3, timestamp) AS version
FROM trvl_heartbeat ${windowed(30)}
GROUP BY product, index1`,
  latestRuntime: `SELECT blob1 AS product, argMax(blob4, timestamp) AS runtime
FROM trvl_heartbeat ${windowed(30)}
GROUP BY product, index1`,
  hours: `SELECT blob1 AS product, toHour(timestamp) AS hour, count() AS points, count(DISTINCT index1) AS installs
FROM trvl_heartbeat ${windowed(30)}
GROUP BY product, hour`,
  hour22: `SELECT blob1 AS product, count() AS points, count(DISTINCT toDate(timestamp)) AS days
FROM trvl_heartbeat ${windowed(30)}
  AND toHour(timestamp) = 22
GROUP BY product, index1`,
  activeDays: `SELECT blob1 AS product, count(DISTINCT toDate(timestamp)) AS days
FROM trvl_heartbeat ${windowed(30)}
GROUP BY product, index1`,
  weekVersion: `SELECT blob1 AS product, toStartOfWeek(timestamp) AS week, argMax(blob3, timestamp) AS version
FROM trvl_heartbeat ${COUNTED}
GROUP BY product, index1, week`,
  retention: `SELECT blob1 AS product, toStartOfWeek(timestamp) AS week, index1 AS install
FROM trvl_heartbeat ${COUNTED}
GROUP BY product, install, week`,
};

const COUNTRY_NAMES = {
  DE: "Germany", IE: "Ireland", US: "United States", IT: "Italy", ES: "Spain", RU: "Russia",
  NL: "Netherlands", FR: "France", SE: "Sweden", IN: "India", CN: "China", GB: "United Kingdom",
  JP: "Japan", CH: "Switzerland", SG: "Singapore", BR: "Brazil", HK: "Hong Kong", NZ: "New Zealand",
  AU: "Australia", KR: "South Korea", ZA: "South Africa", IL: "Israel", PT: "Portugal", MX: "Mexico",
  BE: "Belgium", CZ: "Czechia", AE: "United Arab Emirates", AT: "Austria", CA: "Canada", FI: "Finland",
  RS: "Serbia", LK: "Sri Lanka", EE: "Estonia", LV: "Latvia", PL: "Poland", RO: "Romania", TR: "Turkey",
  BD: "Bangladesh", BG: "Bulgaria", GT: "Guatemala", DK: "Denmark", TH: "Thailand", MY: "Malaysia",
  LU: "Luxembourg", KH: "Cambodia", PH: "Philippines", KZ: "Kazakhstan", GE: "Georgia", VN: "Vietnam",
};

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

function num(value) {
  const n = Number(value);
  return Number.isFinite(n) ? n : 0;
}

function productOf(row) {
  if (!row || row.product == null || row.product === "") return "trvl";
  return String(row.product);
}

function list(raw, ...keys) {
  for (const key of keys) if (Array.isArray(raw[key])) return raw[key];
  return [];
}

function dayText(iso) {
  const [year, month, day] = String(iso).slice(0, 10).split("-");
  const index = Number(month) - 1;
  if (!year || !day || index < 0 || index > 11) return String(iso).slice(0, 10);
  return `${Number(day)} ${MONTHS[index]}`;
}

function weekId(value) {
  return String(value ?? "").slice(0, 10);
}

function isOpenWeek(iso, today) {
  const start = Date.parse(`${iso}T00:00:00Z`);
  const now = Date.parse(`${today}T00:00:00Z`);
  return Number.isFinite(start) && Number.isFinite(now) && now >= start && now < start + 7 * 86400000;
}

function countryName(code) {
  const key = String(code || "").toUpperCase();
  if (!key) return "No country";
  return COUNTRY_NAMES[key] || key;
}

function archName(runtime) {
  const [os, arch] = String(runtime || "").split("/");
  if (os === "linux" && arch === "amd64") return "Linux, 64-bit";
  if (os === "linux" && (arch === "arm64" || arch === "aarch64")) return "Linux, ARM";
  if ((os === "darwin" || os === "macos") && (arch === "arm64" || arch === "aarch64")) return "Mac, Apple silicon";
  if ((os === "darwin" || os === "macos") && (arch === "amd64" || arch === "x86_64")) return "Mac, Intel";
  if (os === "windows" && (arch === "amd64" || arch === "x86_64")) return "Windows, 64-bit";
  return String(runtime || "unknown");
}

function tally(rows, keyFn) {
  const counts = new Map();
  for (const row of rows) {
    const key = keyFn(row);
    counts.set(key, (counts.get(key) || 0) + 1);
  }
  return [...counts.entries()].sort((a, b) => b[1] - a[1] || String(a[0]).localeCompare(String(b[0])));
}

function bucketDays(values) {
  const buckets = [["1 day", 0], ["2 to 3 days", 0], ["4 to 7 days", 0], ["8 to 14 days", 0], ["15 days or more", 0]];
  for (const days of values) {
    const n = num(days);
    if (n <= 1) buckets[0][1] += 1;
    else if (n <= 3) buckets[1][1] += 1;
    else if (n <= 7) buckets[2][1] += 1;
    else if (n <= 14) buckets[3][1] += 1;
    else buckets[4][1] += 1;
  }
  return buckets.filter((row) => row[1] > 0);
}

function positioned(place) {
  if (!place.name || place.name === "No city") return place;
  const cityHit = locate(place.name, place.code);
  if (cityHit) return { ...place, lat: cityHit.lat, lon: cityHit.lon, precision: "city" };
  const countryHit = locate("", place.code);
  if (countryHit) return { ...place, lat: countryHit.lat, lon: countryHit.lon, precision: "country" };
  return place;
}

function placesFrom(rows) {
  if (rows.some((row) => row.installs != null)) {
    return rows
      .map((row) => ({
        name: row.city ? String(row.city) : "No city",
        code: row.country ? String(row.country).toUpperCase() : "",
        value: num(row.installs),
      }))
      .sort((a, b) => b.value - a.value || a.name.localeCompare(b.name));
  }
  const counts = new Map();
  for (const row of rows) {
    const name = row.city ? String(row.city) : "No city";
    const code = row.country ? String(row.country).toUpperCase() : "";
    const key = `${name}|${code}`;
    const current = counts.get(key) || { name, code, value: 0 };
    current.value += 1;
    counts.set(key, current);
  }
  return [...counts.values()].sort((a, b) => b.value - a.value || a.name.localeCompare(b.name));
}

function productModel(name, raw, today) {
  const own = (rows) => rows.filter((row) => productOf(row) === name);
  const window90 = list(raw, "products", "byProduct").find((row) => productOf(row) === name);
  const window30 = list(raw, "window30").find((row) => productOf(row) === name);
  const window7 = list(raw, "window7").find((row) => productOf(row) === name);
  const latest = own(list(raw, "latestPlace"));
  const placeRows = placesFrom(latest.length ? latest : own(list(raw, "places", "byPlace"))).map(positioned);
  const countries = new Map();
  for (const place of placeRows) {
    const current = countries.get(place.code) || { name: countryName(place.code), code: place.code, value: 0 };
    current.value += place.value;
    countries.set(place.code, current);
  }
  const weekCounts = new Map();
  for (const row of own(list(raw, "weeks"))) {
    const id = weekId(row.week);
    if (id) weekCounts.set(id, num(row.installs));
  }
  const versionByWeek = new Map();
  for (const row of own(list(raw, "weekVersion"))) {
    const id = weekId(row.week);
    if (!id) continue;
    const versions = versionByWeek.get(id) || new Map();
    const version = String(row.version || "unknown");
    versions.set(version, (versions.get(version) || 0) + 1);
    versionByWeek.set(id, versions);
  }
  const weekIds = [...new Set([...weekCounts.keys(), ...versionByWeek.keys()])].sort();
  const weeks = weekIds.map((id) => ({
    id,
    label: dayText(id),
    value: weekCounts.get(id) || [...(versionByWeek.get(id) || new Map()).values()].reduce((a, n) => a + n, 0),
    open: isOpenWeek(id, today),
    versions: [...(versionByWeek.get(id) || new Map()).entries()].sort((a, b) => b[1] - a[1]),
  }));
  const sets = new Map();
  for (const row of own(list(raw, "retention"))) {
    const id = weekId(row.week);
    if (!id || row.install == null || row.install === "") continue;
    const set = sets.get(id) || new Set();
    set.add(String(row.install));
    sets.set(id, set);
  }
  const back = [];
  const ids = [...sets.keys()].sort();
  for (let i = 0; i < ids.length - 1; i += 1) {
    if (isOpenWeek(ids[i + 1], today)) continue;
    const from = sets.get(ids[i]);
    const to = sets.get(ids[i + 1]);
    let came = 0;
    for (const install of from) if (to.has(install)) came += 1;
    back.push([`${dayText(ids[i])} to ${dayText(ids[i + 1])}`, came, from.size]);
  }
  const hours = Array(24).fill(0);
  for (const row of own(list(raw, "hours"))) hours[num(row.hour)] = num(row.points);
  const repeat = new Map();
  let hourPoints = 0;
  for (const row of own(list(raw, "hour22"))) {
    const points = num(row.points);
    hourPoints += points;
    const days = row.days == null ? points : num(row.days);
    repeat.set(days, (repeat.get(days) || 0) + 1);
  }
  return {
    weekly: window7 ? num(window7.installs) : 0,
    monthly: window30 ? num(window30.installs) : 0,
    window: window90 ? num(window90.installs) : placeRows.reduce((a, row) => a + row.value, 0),
    weeks,
    days: own(list(raw, "days", "byDay")).map((row) => [weekId(row.day), num(row.installs)]).filter((row) => row[0]).sort((a, b) => a[0].localeCompare(b[0])),
    dayBuckets: bucketDays(own(list(raw, "activeDays")).map((row) => row.days)),
    versions: tally(own(list(raw, "latestVersion")), (row) => String(row.version || "unknown")),
    arch: tally(own(list(raw, "latestRuntime")), (row) => archName(row.runtime)),
    countries: [...countries.values()].sort((a, b) => b.value - a.value || a.name.localeCompare(b.name)),
    places: placeRows,
    hours,
    hour22: {
      points: hourPoints,
      installs: own(list(raw, "hour22")).length,
      repeat: [...repeat.entries()].sort((a, b) => a[0] - b[0]),
    },
    back,
    installed: own(list(raw, "installed", "byInstalled")).filter((row) => row.installed).map((row) => [String(row.installed), num(row.installs)]).sort((a, b) => a[0].localeCompare(b[0])),
  };
}

export function shape(raw = {}, today = new Date().toISOString().slice(0, 10)) {
  const names = [];
  const seen = new Set();
  const add = (name) => {
    if (!name || seen.has(name)) return;
    seen.add(name);
    names.push(name);
  };
  for (const key of ["products", "byProduct", "window7", "window30", "days", "byDay", "places", "byPlace", "latestPlace"]) {
    for (const row of list(raw, key)) add(productOf(row));
  }
  if (names.length === 0) names.push("trvl");
  const by = {};
  for (const name of names) by[name] = productModel(name, raw, today);
  return { read: today, order: names, by };
}

function page(model) {
  const json = JSON.stringify(model)
    .replaceAll("&", "\\u0026")
    .replaceAll("<", "\\u003c")
    .replaceAll(">", "\\u003e")
    .replaceAll("\u2028", "\\u2028")
    .replaceAll("\u2029", "\\u2029");
  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Counts</title>
<link rel="stylesheet" href="https://unpkg.com/leaflet@1.9.4/dist/leaflet.css">
<script src="https://unpkg.com/leaflet@1.9.4/dist/leaflet.js"></script>
<style>
  * { box-sizing: border-box; }
  body { margin: 0; background: #fff; color: #1a1a1a; font: 15px/1.45 ui-sans-serif, system-ui, sans-serif; }
  main { width: min(1100px, calc(100% - 48px)); margin: 0 auto; padding: 48px 0 80px; }
  h1 { margin: 12px 0; font: 56px/1.05 Palatino, Georgia, serif; font-weight: 500; }
  h2 { margin: 0 0 12px; color: #999; font-size: 12px; letter-spacing: 0.12em; text-transform: uppercase; font-weight: 500; }
  .deck, .note, small { color: #666; }
  .chips { display: flex; gap: 8px; flex-wrap: wrap; margin: 12px 0; }
  button.chip, .crumb button { appearance: none; border: 0; background: #f7f7f7; border-radius: 999px; padding: 6px 12px; cursor: pointer; font: inherit; }
  button.chip[aria-pressed="true"], .crumb button[aria-current="true"] { background: #1a1a1a; color: #fff; }
  .metrics { display: grid; grid-template-columns: repeat(4, 1fr); gap: 24px; margin: 28px 0; }
  .metric { appearance: none; border: 0; background: transparent; padding: 0; text-align: left; cursor: pointer; color: inherit; font: inherit; }
  .metric b { display: block; font: 52px/1 Palatino, Georgia, serif; font-weight: 500; }
  section { margin: 0 0 36px; }
  .find { width: min(280px, 100%); margin: 0 0 12px; border: 0; border-bottom: 1px solid #e5e5e5; padding: 8px 0; font: inherit; }
  .row, .more { display: grid; grid-template-columns: minmax(120px, 240px) 1fr 72px; gap: 16px; align-items: center; width: 100%; margin: 0 0 8px; padding: 0; border: 0; background: transparent; color: inherit; font: inherit; text-align: left; cursor: pointer; }
  .row span:last-child, .more span:last-child { font: 13px ui-monospace, monospace; text-align: right; }
  .track { height: 8px; background: #f7f7f7; border-radius: 4px; }
  .bar { height: 8px; background: #1a1a1a; border-radius: 4px; }
  .row[aria-pressed="true"] .bar { background: #0066ff; }
  .more { color: #666; }
  .series { display: flex; align-items: flex-end; gap: 8px; height: 180px; }
  .col { appearance: none; border: 0; background: transparent; height: 100%; flex: 1 1 0; min-width: 0; display: flex; flex-direction: column; justify-content: flex-end; align-items: center; gap: 6px; padding: 0; cursor: pointer; color: inherit; font: inherit; }
  .col i { display: block; width: min(28px, 80%); background: #1a1a1a; border-radius: 3px; }
  .col[aria-pressed="true"] i { background: #0066ff; }
  .col small { color: #666; font-size: 11px; }
  #map-host { height: 560px; border: 1px solid #e5e5e5; background: #f7f7f7; }
  @media (max-width: 800px) { .metrics { grid-template-columns: 1fr 1fr; } h1 { font-size: 40px; } }
</style>
</head>
<body>
<main id="app"></main>
<script>
const DATA = ${json};
const state = { product: DATA.order[0] || "trvl", country: null, place: null, week: null, hour: null, countriesOpen: false, placesOpen: false, q: "" };
let map, mapLayer, mapFrame = "";
function view() { return DATA.by[state.product]; }
function sum(rows) { return rows.reduce((total, row) => total + row.value, 0); }
function counted(n, one, many) { return n + " " + (n === 1 ? one : many); }
function maxOf(rows) { return rows.reduce((total, row) => Math.max(total, row.value || 0), 1); }
function esc(value) { return String(value).replace(/[&<>"]/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[ch]); }
function placesOf(code) { return view().places.filter((row) => row.code === code); }
function shown(rows, open) {
  const query = state.q.trim().toLowerCase();
  const matched = query ? rows.filter((row) => (row.name + " " + (row.code || "")).toLowerCase().includes(query)) : rows;
  if (query || open) return { rows: matched, hidden: [] };
  return { rows: matched.slice(0, 8), hidden: matched.slice(8) };
}
function pairRows(pairs) {
  const scale = maxOf(pairs.map((pair) => ({ value: pair[1] })));
  return pairs.map(([name, value]) => '<div class="row" style="cursor:default"><span>' + esc(name) + '</span><span class="track"><span class="bar" style="width:' + Math.max(0.4, Math.round(value / scale * 1000) / 10) + '%"></span></span><span>' + value + "</span></div>").join("");
}
function clickRows(rows, hidden, kind) {
  const scale = maxOf(rows.concat(hidden));
  const body = rows.map((row) => {
    const key = kind === "country" ? row.code : row.name + "|" + (row.code || "");
    const pressed = kind === "country" ? state.country === row.code : state.place === key;
    const width = Math.max(0.4, Math.round(row.value / scale * 1000) / 10);
    return '<button class="row" aria-pressed="' + pressed + '" data-act="' + kind + '" data-key="' + esc(key) + '"><span>' + esc(row.name) + '</span><span class="track"><span class="bar" style="width:' + width + '%"></span></span><span>' + row.value + "</span></button>";
  }).join("");
  if (!hidden.length) return body;
  const hiddenSum = sum(hidden);
  const width = Math.max(0.4, Math.round(hiddenSum / scale * 1000) / 10);
  const noun = kind === "country" ? ["country", "countries"] : ["place", "places"];
  return body + '<button class="more" data-act="open" data-key="' + kind + '"><span>Show ' + counted(hidden.length, noun[0], noun[1]) + '</span><span class="track"><span class="bar" style="width:' + width + '%"></span></span><span>' + hiddenSum + "</span></button>";
}
function syncMap() {
  const slot = document.getElementById("map-slot");
  if (!slot || !window.L) return;
  let host = document.getElementById("map-host");
  if (!host) { host = document.createElement("div"); host.id = "map-host"; }
  slot.appendChild(host);
  if (!map) {
    map = L.map(host, { minZoom: 2, maxZoom: 8, worldCopyJump: true }).setView([25, 10], 2);
    L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", { attribution: "&copy; OpenStreetMap", maxZoom: 8 }).addTo(map);
    mapLayer = L.layerGroup().addTo(map);
    map.on("zoomend", labelMap);
  }
  mapLayer.clearLayers();
  const rows = (state.country ? placesOf(state.country) : view().places).filter((row) => row.lat != null);
  const biggest = rows.reduce((total, row) => Math.max(total, row.value), 1);
  for (const row of rows) {
    const marker = L.circleMarker([row.lat, row.lon], { radius: 7 + 22 * Math.sqrt(row.value / biggest), color: "#fff", weight: 1, fillColor: "#1a1a1a", fillOpacity: 0.9 });
    marker.options.label = row.name + (row.code ? ", " + row.code : "") + " · " + row.value + (row.precision === "country" ? " · country centre" : "");
    marker.options.installs = row.value;
    marker.on("click", () => { state.country = row.code || null; state.place = row.name + "|" + row.code; state.placesOpen = true; render(); });
    marker.addTo(mapLayer);
  }
  labelMap();
  const frame = state.product + ":" + (state.country || "world");
  if (frame !== mapFrame) {
    mapFrame = frame;
    if (state.country && rows.length) map.fitBounds(L.latLngBounds(rows.map((row) => [row.lat, row.lon])).pad(0.4), { maxZoom: 6 });
    else map.setView([25, 10], 2);
  }
  setTimeout(() => map.invalidateSize(), 0);
}
function labelMap() {
  if (!map || !mapLayer) return;
  const floor = map.getZoom() >= 6 ? 1 : map.getZoom() >= 4 ? 5 : 19;
  mapLayer.eachLayer((marker) => { marker.unbindTooltip(); marker.bindTooltip(marker.options.label, { permanent: marker.options.installs >= floor, direction: "top", opacity: 0.95 }); });
}
function render() {
  const focus = document.activeElement;
  const focusId = focus && focus.id;
  const caret = focus && focus.selectionStart;
  const kept = document.getElementById("map-host");
  if (kept) document.body.appendChild(kept);
  const data = view();
  const country = data.countries.find((row) => row.code === state.country);
  const placeRows = state.country ? placesOf(state.country) : data.places;
  const countryView = shown(data.countries, state.countriesOpen);
  const placeView = shown(placeRows, state.placesOpen || Boolean(state.country));
  const weekScale = maxOf(data.weeks);
  const hourScale = Math.max(1, ...data.hours);
  const weeks = data.weeks.map((week) => '<button class="col" aria-pressed="' + (state.week === week.id) + '" data-act="week" data-key="' + esc(week.id) + '"><b style="font:13px ui-monospace,monospace">' + week.value + '</b><i class="bar" style="height:' + Math.max(8, Math.round(week.value / weekScale * 140)) + "px;background:" + (week.open ? "#0066ff" : "#1a1a1a") + '"></i><small>' + esc(week.label) + "</small></button>").join("");
  const picked = data.weeks.find((week) => week.id === state.week);
  const hours = data.hours.map((value, hour) => '<button class="col" aria-pressed="' + (state.hour === String(hour)) + '" data-act="hour" data-key="' + hour + '"><i class="bar" style="height:' + Math.max(4, Math.round(value / hourScale * 100)) + "px;background:" + (state.hour === String(hour) ? "#0066ff" : "#1a1a1a") + '"></i><small>' + hour + "</small></button>").join("");
  const hourDetail = state.hour == null ? "" : state.hour === "22"
    ? '<p class="note">' + counted(data.hour22.points, "heartbeat", "heartbeats") + " from " + counted(data.hour22.installs, "installation", "installations") + ". The number is different days in that hour.</p>" + pairRows(data.hour22.repeat.map(([days, installs]) => [counted(installs, "installation", "installations"), days]))
    : '<p class="note">' + state.hour + ":00 UTC has " + (data.hours[Number(state.hour)] || 0) + " heartbeats. The per-installation split is counted for 22:00.</p>";
  const chips = DATA.order.map((name) => '<button class="chip" data-act="product" data-key="' + esc(name) + '" aria-pressed="' + (state.product === name) + '">' + esc(name) + "</button>").join("");
  const ratio = data.monthly ? Math.round(100 * data.weekly / data.monthly) : 0;
  const crumb = state.country ? '<div class="crumb chips"><button data-act="clear">All countries</button><button aria-current="true">' + esc(country ? country.name : state.country) + "</button></div>" : "";
  document.getElementById("app").innerHTML = '<p class="deck">Counts · private · ' + esc(DATA.read) + '</p><h1>Active installations</h1><p class="deck">One installation, counted once. Click a product, a country, a folded total, a week, or an hour. City and country are Cloudflare\\'s geolocation of the connecting IP. The IP is not stored. A heartbeat is sent when trvl starts, and only if 24 hours have passed. The only timer in this repo is trvl digest at 08:00 local time.</p><div class="chips">' + chips + '</div><div class="metrics"><button class="metric" data-act="goto" data-key="weeks"><b>' + data.weekly + '</b><span>Weekly</span><small>last 7 days</small></button><button class="metric" data-act="goto" data-key="versions"><b>' + data.monthly + '</b><span>Monthly</span><small>last 30 days</small></button><button class="metric" data-act="goto" data-key="countries"><b>' + data.window + '</b><span>In the window</span><small>last 90 days</small></button><button class="metric" data-act="goto" data-key="back"><b>' + ratio + '%</b><span>Back this week</span><small>' + data.weekly + " of " + data.monthly + ' monthly</small></button></div><section id="weeks"><h2>Each week</h2><div class="series">' + weeks + "</div>" + (picked ? pairRows(picked.versions) : "") + '<p class="note">Sunday weeks. Blue is the week that contains this reading, when that week is not over.</p></section><section id="back"><h2>Came back the next full week</h2>' + pairRows(data.back.map((row) => [row[0], row[1]])) + '<p class="note">' + data.back.map((row) => row[1] + " of " + row[2]).join(". ") + '.</p></section><section id="days"><h2>Days the binary ran · 30 days</h2>' + pairRows(data.dayBuckets) + '</section><section id="versions"><h2>Latest version · 30 days</h2>' + pairRows(data.versions) + '</section><section id="arch"><h2>Architecture · 30 days</h2>' + pairRows(data.arch) + '</section><section id="installed"><h2>Install day carried on the heartbeat</h2>' + pairRows(data.installed) + '</section><hr class="rule"><section id="map"><h2>Where the connecting IP was placed</h2><div id="map-slot"></div><p class="note">The number is installations. The position is the city centre, or the country centre when the city is not in the directory.</p></section><section id="countries"><h2>Country</h2><input id="q" class="find" placeholder="Find a country or place" value="' + esc(state.q) + '">' + clickRows(countryView.rows, countryView.hidden, "country") + '<p class="note">' + counted(data.countries.length, "country", "countries") + ". They add up to " + sum(data.countries) + '.</p></section><section id="places"><h2>Place</h2>' + crumb + clickRows(placeView.rows, placeView.hidden, "place") + '<p class="note">' + counted(placeRows.length, "place", "places") + ". They add up to " + sum(placeRows) + '.</p></section><section id="hours"><h2>Hour the heartbeat landed · UTC · 30 days</h2><div class="series">' + hours + "</div>" + hourDetail + '<p class="note">The time is when the trvl process started. The client has no send clock. trvl digest runs at 08:00 local time.</p></section>';
  syncMap();
  const el = focusId && document.getElementById(focusId);
  if (el) { el.focus(); if (caret != null && el.setSelectionRange) el.setSelectionRange(caret, caret); }
}
document.getElementById("app").addEventListener("click", (event) => {
  const node = event.target.closest("[data-act]");
  if (!node) return;
  const act = node.dataset.act;
  const key = node.dataset.key;
  if (act === "goto") { document.getElementById(key).scrollIntoView({ behavior: "smooth", block: "start" }); return; }
  if (act === "product") { state.product = key; state.country = null; state.place = null; state.week = null; mapFrame = ""; }
  if (act === "open") { if (key === "country") state.countriesOpen = true; if (key === "place") state.placesOpen = true; }
  if (act === "country") { state.country = state.country === key ? null : key; state.place = null; state.placesOpen = false; render(); const section = document.getElementById("places"); if (section) section.scrollIntoView({ behavior: "smooth", block: "start" }); return; }
  if (act === "place") state.place = state.place === key ? null : key;
  if (act === "week") state.week = state.week === key ? null : key;
  if (act === "hour") state.hour = state.hour === key ? null : key;
  if (act === "clear") { state.country = null; state.place = null; mapFrame = ""; }
  render();
});
document.getElementById("app").addEventListener("input", (event) => { if (event.target.id !== "q") return; state.q = event.target.value; render(); });
render();
</script>
</body>
</html>`;
}

export function renderDashboard(raw, today) {
  return page(raw && raw.by ? raw : shape(raw || {}, today));
}
