# Install counts

Private page at `https://counts.revaluator.ai`. Cloudflare Access admits only the author, using the existing “Spark access” policy. The worker verifies the Access login signature. A header that is missing, expired, signed by someone else, or meant for another site is refused before any count is read.

The page reads `trvl_heartbeat` through the Analytics Engine SQL API. The read token is the Worker secret `ANALYTICS_READ_TOKEN`. It is not in this repo. Heartbeats from every product share that dataset, and `blob1` is the product name. Each product stays on its own. The page shows the last 7, 30, and 90 days, then weeks, how many came back the next full week, days the binary ran, latest version, architecture, the install day carried on the heartbeat, country, and place. Click a product to switch. Click a country to filter the places and frame the map. “Show N” opens the rest of a list. Click a week to see the versions in it. Click an hour to read that hour. The 22:00 bar splits into how many installations appeared on how many different days.

A version, a place, and a runtime are the latest heartbeat in the window, so those rows add up to the installations. City and country are Cloudflare’s geolocation of the connecting IP. The IP is not stored. The map is Leaflet on OpenStreetMap tiles, zoomed no closer than a city. A position is the GeoNames city centre, or the country centre when that city is not in the directory. The heartbeat does not store coordinates. Install ids are a grouping key only. The retention query reads one as `install` and drops it before the page is written. `blob9`, the machine id, is not written into the page. Receiver proof rows are left out. `workers.dev` and preview hostnames are disabled. Invocation logs are off.

The heartbeat receiver stays on `telemetry.revaluator.ai`. This hostname is only the page.

```sh
node --test src/index.test.js
npx wrangler@4.111.0 deploy
```

Wrangler 4.111.0. Do not upgrade it as part of a deploy.
