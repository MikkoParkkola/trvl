// Receiver for the trvl daily heartbeat. Same accept/reject rules as
// cmd/trvl-telemetry: POST /v1/heartbeat, application/json, at most 2048
// bytes, and only the five wire fields. Accepted points go to Analytics
// Engine. The connection IP is not copied into the point. Cloudflare's
// geolocation of that connection supplies a city name and a country code,
// and those two strings are stored with the point. Coordinates and the
// rest of request.cf are not.

export const MAX_PAYLOAD = 2048;

const ALLOWED = ["project", "event", "version", "runtime", "install_id"];
const ALLOWED_SET = new Set(ALLOWED);
// Analytics Engine drops a point whose index is longer than 96 bytes.
const MAX_INDEX_BYTES = 96;

function mediaType(header) {
  if (!header) return "";
  const parts = String(header).split(";");
  const type = parts[0].trim().toLowerCase();
  if (!type.includes("/")) return "";
  for (let i = 1; i < parts.length; i++) {
    const param = parts[i].trim();
    if (param === "") continue;
    if (!param.includes("=")) return "";
  }
  return type;
}

function byteLength(value) {
  return new TextEncoder().encode(value).byteLength;
}

function screen(method, pathname, contentType) {
  if (pathname !== "/v1/heartbeat") return { status: 404, body: "not found\n" };
  if (method !== "POST") return { status: 405, body: "method not allowed\n" };
  if (mediaType(contentType) !== "application/json") {
    return { status: 415, body: "unsupported media type\n" };
  }
  return null;
}

// decide is the pure contract check. A 204 result carries the five fields
// and nothing else. Every other status carries no record, so the caller
// must not write.
export function decide(method, pathname, contentType, body) {
  const early = screen(method, pathname, contentType);
  if (early) return early;
  const text = typeof body === "string" ? body : "";
  if (byteLength(text) > MAX_PAYLOAD) {
    return { status: 413, body: "payload too large\n" };
  }
  let raw;
  try {
    raw = JSON.parse(text);
  } catch {
    return { status: 400, body: "malformed json\n" };
  }
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) {
    return { status: 400, body: "malformed json\n" };
  }
  for (const key of Object.keys(raw)) {
    if (!ALLOWED_SET.has(key)) return { status: 400, body: "unexpected field\n" };
  }
  const record = {};
  for (const key of ALLOWED) {
    if (!Object.hasOwn(raw, key) || raw[key] === null) {
      record[key] = "";
      continue;
    }
    if (typeof raw[key] !== "string") {
      return { status: 400, body: "malformed json\n" };
    }
    record[key] = raw[key];
  }
  if (record.project === "" || record.event === "") {
    return { status: 400, body: "missing required field\n" };
  }
  return { status: 204, record };
}

function text(decision) {
  return new Response(decision.body, {
    status: decision.status,
    headers: { "content-type": "text/plain; charset=utf-8" },
  });
}

async function readCapped(request, max) {
  if (request.body == null) return { oversize: false, text: "" };
  const reader = request.body.getReader();
  const chunks = [];
  let kept = 0;
  const limit = max + 1;
  let oversize = false;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      if (!value || value.byteLength === 0) continue;
      const room = limit - kept;
      if (value.byteLength > room) {
        chunks.push(value.subarray(0, room));
        oversize = true;
        break;
      }
      chunks.push(value);
      kept += value.byteLength;
      if (kept > max) {
        oversize = true;
        break;
      }
    }
  } finally {
    try {
      await reader.cancel();
    } catch {
      // The body is already finished.
    }
  }
  if (oversize) return { oversize: true, text: "" };
  const merged = new Uint8Array(kept);
  let offset = 0;
  for (const chunk of chunks) {
    merged.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return { oversize: false, text: new TextDecoder().decode(merged) };
}

function cfString(cf, key) {
  if (cf == null || typeof cf !== "object") return "";
  const value = cf[key];
  return typeof value === "string" ? value : "";
}

// City and country come only from the runtime geolocation object. A missing
// or non-string value is stored as empty. Nothing else on that object is read.
function connectionPlace(request) {
  const cf = request == null ? undefined : request.cf;
  return { city: cfString(cf, "city"), country: cfString(cf, "country") };
}

function writePoint(env, record, place) {
  const idBytes = byteLength(record.install_id);
  const index = idBytes > 0 && idBytes <= MAX_INDEX_BYTES ? record.install_id : "trvl";
  env.HEARTBEAT.writeDataPoint({
    indexes: [index],
    blobs: [
      record.project,
      record.event,
      record.version,
      record.runtime,
      record.install_id,
      place.city,
      place.country,
    ],
  });
}

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    const contentType = request.headers.get("content-type") ?? "";
    const early = screen(request.method, url.pathname, contentType);
    if (early) return text(early);

    let read;
    try {
      read = await readCapped(request, MAX_PAYLOAD);
    } catch {
      return text({ status: 400, body: "read error\n" });
    }
    if (read.oversize) return text({ status: 413, body: "payload too large\n" });

    const decision = decide(request.method, url.pathname, contentType, read.text);
    if (decision.record) {
      try {
        writePoint(env, decision.record, connectionPlace(request));
      } catch {
        return text({ status: 500, body: "storage error\n" });
      }
    }
    if (decision.status === 204) return new Response(null, { status: 204 });
    return text(decision);
  },
};
