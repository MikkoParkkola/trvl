# trvl install counts

Private page at `https://counts.revaluator.ai`. Cloudflare Access admits only the author, using the existing “Spark access” policy. The worker verifies the Access login signature. A header that is missing, expired, signed by someone else, or meant for another site is refused before any count is read.

The page reads `trvl_heartbeat` through the Analytics Engine SQL API. The read token is the Worker secret `ANALYTICS_READ_TOKEN`. It is not in this repo. The page shows distinct installs by day and by stored city and country, and draws those cities on a map framed to fit them. The map uses a GeoNames city directory and a Natural Earth coastline. The heartbeat still does not store coordinates. Install ids are not shown. Receiver proof rows are left out. `workers.dev` and preview hostnames are disabled. Invocation logs are off.

The heartbeat receiver stays on `telemetry.revaluator.ai`. This hostname is only the page.

```sh
node --test src/index.test.js
wrangler deploy
```

Wrangler 4.111.0. Do not upgrade it as part of a deploy.
