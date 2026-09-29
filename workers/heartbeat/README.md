# trvl heartbeat receiver

Accepts `POST /v1/heartbeat` on `https://telemetry.revaluator.ai/v1/heartbeat`.

The body may contain only `project`, `event`, `version`, `runtime`, and `install_id`. Anything else is rejected. A valid body returns 204 and writes one Analytics Engine point: those five strings as blobs, and `install_id` as the index when it is 1–96 bytes. An empty or longer id is indexed as `trvl` and the id itself stays in the blob.

The Worker does not read the client IP, country, or request headers into that point. Invocation logs are off, so the dataset is the only store this Worker writes. Cloudflare still terminates TLS, so Cloudflare can see the connection IP.

`workers.dev` and preview hostnames are disabled. A released build dials this host. `TRVL_TELEMETRY_ENDPOINT` replaces it.

```sh
node --test
wrangler deploy
```

Wrangler 4.111.0. Deploy uses the logged-in account in the config. It creates the DNS record and certificate for `telemetry.revaluator.ai`.
