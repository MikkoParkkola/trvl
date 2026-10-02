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

function boxAttr(box) {
  return `${box.x.toFixed(2)} ${box.y.toFixed(2)} ${box.w.toFixed(2)} ${box.h.toFixed(2)}`;
}

// A mark is a share of the view. The busiest city is 1.5% of the frame
// width, so on a world map it is a dot. The page keeps that share as the
// view changes, which stops a zoomed-in city from filling the screen.
export function heatRadius(installs, maxCount, frameWidth) {
  const count = Math.max(0, Number(installs) || 0);
  const max = Math.max(1, Number(maxCount) || 1);
  const weight = Math.sqrt(count / max);
  const width = Math.max(1, Number(frameWidth) || 1);
  return width * (0.006 + 0.009 * weight);
}

export function mapSvg(points, mark = "heat") {
  const box = frame(points);
  const maxCount = Math.max(1, ...points.map((point) => point.installs));
  const heatId = String(mark).replace(/[^a-z0-9-]/gi, "") || "heat";
  const labelSize = (box.w * 0.018).toFixed(2);
  const labelLift = box.w * 0.028;
  const blobs = points
    .map((point) => {
      const [x, y] = project(point.lat, point.lon);
      const radius = heatRadius(point.installs, maxCount, box.w);
      const weight = Math.sqrt(Math.max(0, Number(point.installs) || 0) / maxCount);
      const opacity = (0.55 + 0.45 * weight).toFixed(2);
      const span = (radius / box.w).toFixed(5);
      return `<circle class="heat" data-span="${span}" style="fill:url(#${heatId})" cx="${x.toFixed(2)}" cy="${y.toFixed(2)}" r="${radius.toFixed(2)}" fill-opacity="${opacity}"><title>${escapeHtml(point.label)}: ${escapeHtml(point.installs)}</title></circle>`;
    })
    .join("");
  const labels =
    points.length > 0 && points.length <= 12
      ? points
          .map((point) => {
            const [x, y] = project(point.lat, point.lon);
            const caption = `${point.label} · ${point.installs}`;
            const half = caption.length * box.w * 0.018 * 0.32;
            let lx = x;
            const left = box.x + half;
            const right = box.x + box.w - half;
            if (right > left) lx = Math.max(left, Math.min(right, x));
            const above = y - labelLift;
            const ly = above < box.y + labelLift ? y + labelLift * 1.15 : above;
            return `<text class="label" data-px="${x.toFixed(2)}" data-py="${y.toFixed(2)}" x="${lx.toFixed(2)}" y="${ly.toFixed(2)}" font-size="${labelSize}">${escapeHtml(point.label)} · ${escapeHtml(point.installs)}</text>`;
          })
          .join("")
      : "";
  const empty = points.length === 0 ? `<text class="empty" x="${(box.x + box.w / 2).toFixed(2)}" y="${(box.y + box.h / 2).toFixed(2)}" font-size="${(box.w * 0.028).toFixed(2)}">No city stored yet</text>` : "";
  return `<svg class="atlas" viewBox="${boxAttr(box)}" data-home="${boxAttr(box)}" data-world="${boxAttr(FULL)}" aria-label="Where installs are" tabindex="0">
<defs>
  <radialGradient id="${heatId}" cx="50%" cy="50%" r="50%">
    <stop offset="0%" stop-color="#7c2d12" stop-opacity="1"/>
    <stop offset="38%" stop-color="#9a3412" stop-opacity="0.96"/>
    <stop offset="68%" stop-color="#ea580c" stop-opacity="0.42"/>
    <stop offset="100%" stop-color="#fdba74" stop-opacity="0"/>
  </radialGradient>
</defs>
<rect class="sea" x="${FULL.x.toFixed(2)}" y="${FULL.y.toFixed(2)}" width="${FULL.w.toFixed(2)}" height="${FULL.h.toFixed(2)}"/>
<path class="land" d="${LAND}"/>
<g>${blobs}</g>
${labels}${empty}
</svg>`;
}
