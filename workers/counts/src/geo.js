import { CITIES, COUNTRIES } from "./gazetteer.js";

// English spellings Cloudflare uses when the directory's own name differs.
const ALIAS = new Map([
  ["zurich|CH", "zürich|CH"],
  ["cologne|DE", "köln|DE"],
  ["frankfurt|DE", "frankfurt am main|DE"],
  ["seville|ES", "sevilla|ES"],
]);

let cityIndex;

function cities() {
  if (cityIndex) return cityIndex;
  cityIndex = new Map();
  for (const line of CITIES.split("\n")) {
    if (!line) continue;
    const parts = line.split("|");
    const lon = Number(parts.pop());
    const lat = Number(parts.pop());
    const code = parts.pop();
    const name = parts.join("|");
    cityIndex.set(`${name}|${code}`, [lat, lon]);
  }
  return cityIndex;
}

// City name plus country code, or a country code alone. Never a stored coordinate.
export function locate(city, country) {
  const code = String(country || "").trim().toUpperCase();
  const name = String(city || "").trim().toLowerCase();
  if (!code || code.length !== 2) return null;
  if (name) {
    const key = `${name}|${code}`;
    const hit = cities().get(ALIAS.get(key) || key);
    return hit ? { lat: hit[0], lon: hit[1] } : null;
  }
  const countryHit = COUNTRIES[code];
  return countryHit ? { lat: countryHit[0], lon: countryHit[1] } : null;
}
