# Provider trust tiers: decision document

Status: **APPROVED as written, 2026-08-05.** Design A ships; the mainstream-seller
tier of Design B is deferred indefinitely. Tracks issue #535.

Implementation state:

- **Refundability — done.** The seller's cancellation terms are carried through to
  the per-seller price list instead of being discarded, as three states rather
  than two: refundable, stated otherwise, and UNKNOWN when the seller is silent.
  Nil rather than false for unknown, because the upstream flag is a positive
  badge that is simply absent when there is no offer, and rendering that absence
  as "not refundable" would be a claim the source never made. This also lifts the
  self-imposed ceiling described below.
- **Official property site — done.** SerpAPI's current property-details schema
  supplies `official: true` on the seller row itself. trvl preserves that
  positive-only upstream fact through CLI and MCP output; absence stays unknown.
  No hostname matcher or maintained brand list is involved.


## The question

trvl shows a traveller several sellers for the same hotel room and sorts them by
price. Every seller that survives price verification is presented as equally
trustworthy. A traveller may rationally pay more to book with the hotel itself,
with a brand they recognise, or on a rate they can cancel. Today trvl gives them
nothing to make that trade with.

The question is not whether trust matters. It is whether trvl can report a trust
signal it can actually derive from what providers return, or whether it would be
publishing an opinion about which companies are respectable.

A research spike on the ticket already settled the factual ground: refundability
is available for room-level results and is dropped on the way into the
per-provider price list, and the only route to a "mainstream brand" label is a
list somebody maintains by hand.

## Design A: report what the data proves

Two signals, both derived, neither editorial.

*Official property site.* Preserve the source's `official: true` seller flag.
This is positive-only. A row without the flag is "not established as official",
never "not official". No provider name or hostname is interpreted locally.

*Refundability.* Carry the cancellation terms the source already returns through
to the per-seller list instead of discarding them, and mark them unknown when the
source is silent. Never guess from price, brand or rate name.

Cost: roughly half a day, plus one captured live response to test against, since
the repository has no stored fixture for this endpoint yet. Small and reversible.

Buys: the two trade-offs travellers most often make, expressed as fact rather
than judgement. It also unblocks a second thing. The booking-readiness verdict
for per-seller prices currently declares refundability permanently unavailable,
so those results can never reach "ready" no matter how good they are. That
ceiling is self-imposed and this work removes it.

Does not buy: the middle tier. Design A does not satisfy acceptance criterion
`TRVL.TRUST.1` as written, which names three levels including "mainstream OTA".

## Design B: the three-tier taxonomy

Official site, mainstream seller, everything else. The middle tier requires trvl
to hold a list of brands it considers mainstream. Something close to this already
exists in the codebase for a narrower parsing job, which is a preview of the
problem rather than a head start.

Cost: the list is wrong the day it ships and drifts further every month as
brands merge, rebrand and enter new markets. Sellers left out have a fair
complaint, and it is a public one. The maintenance never ends, and it is
judgement work that cannot be delegated to a test. This is a standing editorial
commitment, not a feature.

Buys: it is the only design that meets `TRVL.TRUST.1` word for word, and the
only one that says anything at all about the large middle of the market where
most bookings actually happen.

## Recommendation

Ship Design A. Defer the mainstream-seller tier indefinitely, because the
ticket's own fail-fast clause says to refuse a trust signal that is really a
maintained opinion list, and that is exactly what the middle tier would be.

Default ordering stays on price under both designs. This adds a column, it does
not re-rank anybody's results. Changing the default order is a separate decision
and should stay separate.

## What would make this wrong

*The upstream official flag disappears or changes meaning.* SerpAPI's current
property-details schema and sample explicitly mark the direct Hilton seller row
`official: true`. If that field disappears, trvl falls back to unknown rather
than guessing from domains or names.

*A neutral outside authority appears.* If the data source starts returning a
seller-type flag of its own, or an accreditation identifier becomes reliably
available, the middle tier stops being an opinion and Design B becomes cheap.
Revisit on that trigger, not on a calendar.

*Travellers cannot act on it.* If the official-site label lands on so few rows
that nobody's choice changes, the work bought a column and no decisions. Worth
measuring once it ships rather than arguing now.

## Evidence

Ticket #535 and its research comment. `internal/pricefeed/pricefeed.go:163`
(self-imposed refundability ceiling), `:145-163` and `:177-180` (the two readiness
paths). `internal/hotels/serpapi_fallback.go:336,354-364` (terms dropped) against
`:375` (terms read). `internal/hotels/rooms_parse.go:228-233` (existing hardcoded
brand list). `internal/serpapi/serpapi.go:63` (hotel's own address).
SerpAPI's official Google Hotels Property Details API schema (`official: true`
on a seller row): https://serpapi.com/google-hotels-property-details.
`docs/PUBLIC_ARTICLE_FEEDBACK.md:65-67` (original request).
