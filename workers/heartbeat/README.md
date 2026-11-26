# trvl heartbeat receiver

Accepts `POST /v1/heartbeat` on `https://telemetry.revaluator.ai/v1/heartbeat`.

The body may contain `project`, `event`, `version`, `runtime`, and `install_id`. `install_date` and `machine_id` are optional. `install_date` is a UTC day written `YYYY-MM-DD`. `machine_id` is 32 hex characters. An empty value for either is accepted. A bad value is rejected. Any other field is rejected. A body with the original five fields still returns 204. A valid body returns 204 and writes one Analytics Engine point. The five body strings are blobs, followed by the city name and the country code Cloudflare’s geolocation attaches to the connection, then `install_date` and `machine_id`. `install_id` is the index when it is 1–96 bytes. An empty or longer id is indexed as `trvl` and the id itself stays in the blob.

The Worker does not read the client IP, coordinates, postal code, region, colo, or request headers into that point. A missing or non-string city or country is stored as an empty string. Invocation logs are off, so the dataset is the only store this Worker writes. Cloudflare still terminates TLS, so Cloudflare can see the connection IP. The city is that lookup, not a GPS fix: a VPN, a relay, or a mobile network can place it at the network exit, and some connections have no city.

`workers.dev` and preview hostnames are disabled. A released build dials `https://telemetry.revaluator.ai/v1/heartbeat`. `TRVL_TELEMETRY_ENDPOINT` replaces that URL.

```sh
node --test
wrangler deploy
```

Wrangler 4.111.0. Deploy uses the logged-in account in the config. It creates the DNS record and certificate for `telemetry.revaluator.ai`.
