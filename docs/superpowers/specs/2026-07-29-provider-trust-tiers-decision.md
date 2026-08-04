# Provider trust tiers: decision document

Status: proposed, awaiting operator sign-off. Tracks issue #535. Nothing here is implemented.

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

*Official property site.* When a seller's web address matches the hotel's own
address as the source reported it, and that address is durable rather than a
short-lived redirect, mark the row as the property's own site. This is
positive-only. A row with no match is "not established as official", never "not
official". Most rows will carry no label at all, and that is the honest outcome.

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

*The official-site match does not survive contact with real data.* If the hotel
address the source reports usually resolves to a booking intermediary rather than
the hotel's own domain, particularly for chains, the signal is noise and Design A
collapses to refundability alone. One captured live response settles this, and it
should be captured before any code is written.

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
`internal/hotels/link_durability.go` (durability check).
`docs/PUBLIC_ARTICLE_FEEDBACK.md:65-67` (original request).
