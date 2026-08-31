# trvl-telemetry

This is the server side of the trvl heartbeat. The CLI sends one anonymous
heartbeat per install per day (see `internal/telemetry/heartbeat.go`); this
binary receives those POSTs and writes the accepted ones to a file, one JSON
object per line.

## What it accepts

A heartbeat is a small JSON object with exactly these fields and nothing else:

```json
{"project":"trvl","event":"heartbeat","version":"1.2.3","runtime":"linux/amd64/go1.26.6","install_id":"<hex>"}
```

The collector rejects anything that does not fit the contract:

- not a POST -> 405
- not `application/json` -> 415
- body over 2 KB -> 413
- any field outside the allowed set -> 400 (this is the identity-leak guard)
- malformed JSON or a missing required field -> 400

A valid heartbeat gets a `204 No Content`. The collector never reads or stores
the client IP, hostname, or any request header. Only the payload fields above
reach disk.

## Run it

```sh
go run ./cmd/trvl-telemetry -addr :8080 -out heartbeats.ndjson
```

Both flags have defaults, so plain `go run ./cmd/trvl-telemetry` listens on
`:8080` and appends to `heartbeats.ndjson` in the working directory.

To build a standalone binary:

```sh
go build -o trvl-telemetry ./cmd/trvl-telemetry
./trvl-telemetry -addr :8080 -out /var/lib/trvl/heartbeats.ndjson
```

## Deploy notes

Point the CLI at the running collector through the `TRVL_TELEMETRY_ENDPOINT`
environment variable, or set the production URL as the default endpoint in
`internal/telemetry/heartbeat.go`. The path the binary serves is
`/v1/heartbeat`.

Put it behind a TLS-terminating proxy and keep the output file on durable
storage. The store is a flat append-only NDJSON file, which is enough for a
daily low-volume signal; if traffic ever grows past what one file can handle,
swap the writer for a real datastore. Nothing else in the design assumes the
file format.
