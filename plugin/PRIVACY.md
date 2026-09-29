# Privacy policy for the trvl Claude plugin

This policy covers the trvl plugin in this folder and the trvl program it starts. The author is Mikko Parkkola. Questions and security reports go to GitHub issues: https://github.com/MikkoParkkola/trvl/issues

## What stays on your machine

trvl keeps trips, preferences, the traveller profile, price watches, search history, cached cookies, provider tokens, and a random install id under `~/.trvl`. Those files remain until you delete them. The plugin runs the `trvl` binary already on your PATH. It sends none of your Claude conversation to the author.

## Searches

A search sends the route, the dates, and the traveller count to the travel sites being searched. Default searches reach providers such as Google Flights and Kiwi. Optional providers run when you set their key in the environment. The Air France-KLM key is read from the environment on an ordinary search, and from the macOS Keychain or 1Password only when you explicitly select that provider.

## Daily heartbeat

A released build sends at most one POST per install per day to `https://telemetry.trvl.app/v1/heartbeat`. The JSON has five fields and no others:

- `project`, always `trvl`
- `event`, always `heartbeat`
- the trvl version
- the Go runtime string (operating system, architecture, and Go version)
- `install_id`, a random value stored in `~/.trvl/install-id`

The payload contains no hostname, no username, and no search. The connection shows your IP address to that host, as any HTTPS request does. The project uses the address to derive coarse geography and reports that geography only in groups of at least five. The install id stays the same until you delete `~/.trvl`, so heartbeats from one install can be linked to each other. The id is random and contains none of your name.

Development builds, tests, and CI skip the heartbeat. Set any one of these to turn it off: `TRVL_NO_TELEMETRY=1`, `NO_TELEMETRY=1`, or `DO_NOT_TRACK=1`. `TRVL_TELEMETRY_ENDPOINT` replaces the host when you run your own collector.

No deletion window for heartbeat records is published.

## Webhooks you set

When you attach a webhook URL to a price watch, trvl POSTs that watch's route and price to the address you supplied.
