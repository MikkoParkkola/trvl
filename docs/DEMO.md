# trvl Demo

Last updated: 2026-05-13

The public demo is a fixture-backed one-prompt transcript. It is intentionally stable so the README GIF and cast do not fail when live travel providers throttle or change prices.

Run the full install-plus-planning transcript locally:

```bash
scripts/demo/full-demo.sh
```

Run only the one-prompt planning section:

```bash
scripts/demo/one-prompt-demo.sh
```

The transcript shows one assistant prompt flowing through the `travel` router into:

- flight search with a booking URL;
- hotel detail enrichment with rooms and amenities;
- ground transfer comparison;
- travel hack checks with unsafe hidden-city usage rejected for checked-bag risk;
- optional `watch_price` creation, gated on user confirmation;
- a "Naive -> Optimized -> Saved" comparison.

Render the checked-in artifacts:

```bash
asciinema rec --overwrite -c "scripts/demo/full-demo.sh" demo.cast
agg demo.cast demo.gif
```

The demo is not a booking claim. trvl returns provider URLs, evidence freshness, and booking-readiness checks. Payment, cancellation, and final confirmation stay with the provider unless a future explicitly scoped booking integration is added.
