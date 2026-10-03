# Privacy policy for the trvl Claude plugin

This policy covers the trvl plugin in this folder: three skills, the /trvl command, and the trip-coordinator agent. The plugin does not download a program and does not start one. It sends no data. The trvl program is a separate install (`brew install MikkoParkkola/tap/trvl`). The next section is this plugin. Every section after that describes the separate trvl program. The author is Mikko Parkkola (mikko.parkkola@iki.fi). Questions and security reports go to GitHub issues: https://github.com/MikkoParkkola/trvl/issues

## What the plugin sends

Nothing. It does not read credentials from the machine, does not send the Claude conversation, and does not contact `https://telemetry.revaluator.ai/v1/heartbeat`.

## What the trvl program keeps on your machine

The trvl program keeps trips, preferences, the traveller profile, price watches, search history, cached cookies, provider tokens, and a random install id under `~/.trvl`. Those files remain until you delete them. The plugin does not download that program. The program sends none of your Claude conversation to the author.

## What the trvl program sends on a search

A search sends the route, the dates, and the traveller count to the travel sites being searched. Default searches reach providers such as Google Flights and Kiwi. Optional providers run when you set their key in the environment. The Air France-KLM key is read from the environment on an ordinary search, and from the macOS Keychain or 1Password only when you explicitly select that provider.

## Daily heartbeat from the trvl program

A released build of the trvl program sends at most one POST per install per day to `https://telemetry.revaluator.ai/v1/heartbeat`. The JSON has five fields and no others:

- `project`, always `trvl`
- `event`, always `heartbeat`
- the trvl version
- the Go runtime string (operating system, architecture, and Go version)
- `install_id`, a random value stored in `~/.trvl/install-id`

The payload contains no hostname, no username, and no search. Cloudflare terminates the connection, so Cloudflare can see the caller IP. That address is not written into the stored record. Coordinates are not written into the stored point. From the connection, Cloudflare’s geolocation supplies a city name and a country code, and those two values are stored with the heartbeat. They are approximate: a VPN, a relay, or a mobile network can place the city at the network exit rather than where you are, and some connections have no city. The install id stays the same until you delete `~/.trvl`, so heartbeats from one install can be linked to each other, including the city stored with them. The id is random and contains none of your name. The request times out after 3 seconds, and a failed send is ignored. Cloudflare Analytics Engine keeps the records for three months.

Development builds, tests, and CI skip the heartbeat. Set any one of these to turn it off: `TRVL_NO_TELEMETRY=1`, `NO_TELEMETRY=1`, or `DO_NOT_TRACK=1`. `TRVL_TELEMETRY_ENDPOINT` replaces the whole URL when you run your own collector. An empty value does not.

## Webhooks the trvl program sends

When you attach a webhook URL to a price watch, trvl POSTs that watch's route and price to the address you supplied.
