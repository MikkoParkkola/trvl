import { LAND, WORLD } from "./gazetteer.js";

const LAT_LIMIT = 82;

export function project(lat, lon) {
  const limited = Math.max(-LAT_LIMIT, Math.min(LAT_LIMIT, lat));
  const x = ((lon + 180) / 360) * WORLD;
  const s = Math.sin((limited * Math.PI) / 180);
  const y = (0.5 - Math.log((1 + s) / (1 - s)) / (4 * Math.PI)) * WORLD;
  return [x, y];
}

const Y_NORTH = project(LAT_LIMIT, 0)[1];
const Y_SOUTH = project(-LAT_LIMIT, 0)[1];
const WORLD_H = Y_SOUTH - Y_NORTH;
const FULL = { x: 0, y: Y_NORTH, w: WORLD, h: WORLD_H };

// Frame every point, with a regional minimum so one city is not a street zoom
// and not the whole globe. City names are not precise enough for a street.
export function frame(points) {
  if (points.length === 0) return FULL;
  const xy = points.map((point) => project(point.lat, point.lon));
  let minX = Infinity;
  let maxX = -Infinity;
  let minY = Infinity;
  let maxY = -Infinity;
  for (const [x, y] of xy) {
    minX = Math.min(minX, x);
    maxX = Math.max(maxX, x);
    minY = Math.min(minY, y);
    maxY = Math.max(maxY, y);
  }
  if (maxX - minX > WORLD * 0.5) return FULL;

  const minW = WORLD * (18 / 360);
  let w = Math.max(maxX - minX, minW);
  let h = Math.max(maxY - minY, minW * 0.72);
  const aspect = 1.45;
  if (w / h < aspect) w = h * aspect;
  else h = w / aspect;
  w *= 1.24;
  h *= 1.24;
  if (w >= WORLD || h >= WORLD_H) return FULL;

  let x = (minX + maxX) / 2 - w / 2;
  let y = (minY + maxY) / 2 - h / 2;
  x = Math.max(0, Math.min(WORLD - w, x));
  y = Math.max(Y_NORTH, Math.min(Y_SOUTH - h, y));
  const inside = xy.every(([px, py]) => px >= x && px <= x + w && py >= y && py <= y + h);
  if (!inside) return FULL;
  return { x, y, w, h };
}

function escapeHtml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

export function mapSvg(points) {
  const box = frame(points);
  const maxCount = Math.max(1, ...points.map((point) => point.installs));
  const blur = (box.w * 0.009).toFixed(2);
  const blobs = points
    .map((point) => {
      const [x, y] = project(point.lat, point.lon);
      const weight = point.installs / maxCount;
      const radius = box.w * (0.02 + 0.1 * weight);
      const opacity = (0.5 + 0.5 * weight).toFixed(2);
      return `<circle class="heat" cx="${x.toFixed(2)}" cy="${y.toFixed(2)}" r="${radius.toFixed(2)}" fill-opacity="${opacity}"><title>${escapeHtml(point.label)}: ${escapeHtml(point.installs)}</title></circle>`;
    })
    .join("");
  const labels =
    points.length > 0 && points.length <= 12
      ? points
          .map((point) => {
            const [x, y] = project(point.lat, point.lon);
            const size = (box.w * 0.034).toFixed(2);
            const dy = box.w * 0.012;
            return `<text class="label" x="${x.toFixed(2)}" y="${(y - dy).toFixed(2)}" font-size="${size}">${escapeHtml(point.label)}</text>`;
          })
          .join("")
      : "";
  const empty = points.length === 0 ? `<text class="empty" x="${(box.x + box.w / 2).toFixed(2)}" y="${(box.y + box.h / 2).toFixed(2)}" font-size="${(box.w * 0.04).toFixed(2)}">No city stored yet</text>` : "";
  return `<svg viewBox="${box.x.toFixed(2)} ${box.y.toFixed(2)} ${box.w.toFixed(2)} ${box.h.toFixed(2)}" role="img" aria-label="Where installs are">
<defs>
  <radialGradient id="heat" cx="50%" cy="50%" r="50%">
    <stop offset="0%" stop-color="#7c2d12" stop-opacity="0.92"/>
    <stop offset="28%" stop-color="#c2410c" stop-opacity="0.62"/>
    <stop offset="62%" stop-color="#fdba74" stop-opacity="0.28"/>
    <stop offset="100%" stop-color="#fdba74" stop-opacity="0"/>
  </radialGradient>
  <filter id="soften" x="-80%" y="-80%" width="260%" height="260%">
    <feGaussianBlur stdDeviation="${blur}"/>
  </filter>
</defs>
<rect class="sea" x="${box.x.toFixed(2)}" y="${box.y.toFixed(2)}" width="${box.w.toFixed(2)}" height="${box.h.toFixed(2)}"/>
<path class="land" d="${LAND}"/>
<g filter="url(#soften)">${blobs}</g>
${labels}${empty}
</svg>`;
}
