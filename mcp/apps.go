package mcp

const (
	trvlSearchResultsAppURI = "ui://trvl/search-results.html"
	mcpAppResourceMimeType  = "text/html;profile=mcp-app"
)

var searchResultsAppToolNames = map[string]bool{
	"search_flights":             true,
	"plan_flight_bundle":         true,
	"search_dates":               true,
	"search_accommodations":      true,
	"search_hotels":              true,
	"search_hotels_with_details": true,
	"search_hotel_by_name":       true,
	"hotel_prices":               true,
	"hotel_rooms":                true,
	"plan_trip":                  true,
	"calculate_trip_cost":        true,
	"weekend_getaway":            true,
	"optimize_multi_city":        true,
	"optimize_booking":           true,
	"search_ground":              true,
	"search_airport_transfers":   true,
	"search_cars":                true,
	"search_restaurants":         true,
	"search_deals":               true,
	"search_route":               true,
}

func withSearchResultsApp(tool ToolDef) ToolDef {
	if !searchResultsAppToolNames[tool.Name] {
		return tool
	}
	if tool.Meta == nil {
		tool.Meta = make(map[string]any, 2)
	}
	ui := map[string]any{
		"resourceUri": trvlSearchResultsAppURI,
	}
	tool.Meta["ui"] = ui
	// Compatibility key used by older MCP Apps hosts before nested _meta.ui.
	tool.Meta["ui/resourceUri"] = trvlSearchResultsAppURI
	return tool
}

func searchResultsAppResourceMeta() map[string]any {
	return map[string]any{
		"ui": map[string]any{
			"prefersBorder": true,
			"csp": map[string]any{
				"connectDomains":  []string{},
				"resourceDomains": []string{},
				"frameDomains":    []string{},
			},
		},
	}
}

func trvlSearchResultsAppHTML() string {
	return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>trvl results</title>
  <style>
    :root { color-scheme: light dark; font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    body { margin: 0; padding: 16px; background: Canvas; color: CanvasText; }
    main { display: grid; gap: 14px; }
    h1 { margin: 0; font-size: 18px; line-height: 1.2; }
    .summary { margin: 0; color: color-mix(in srgb, CanvasText 72%, transparent); font-size: 13px; }
    .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 10px; }
    article { border: 1px solid color-mix(in srgb, CanvasText 18%, transparent); border-radius: 8px; padding: 12px; background: color-mix(in srgb, Canvas 94%, CanvasText 6%); }
    article h2 { margin: 0 0 6px; font-size: 14px; }
    dl { display: grid; grid-template-columns: max-content 1fr; gap: 4px 10px; margin: 0; font-size: 12px; }
    dt { color: color-mix(in srgb, CanvasText 62%, transparent); }
    dd { margin: 0; min-width: 0; overflow-wrap: anywhere; }
    .tag { display: inline-block; margin: 2px 4px 2px 0; padding: 2px 6px; border: 1px solid color-mix(in srgb, CanvasText 18%, transparent); border-radius: 999px; font-size: 11px; }
    pre { white-space: pre-wrap; overflow-wrap: anywhere; margin: 0; font-size: 12px; }
  </style>
</head>
<body>
  <main>
    <header>
      <h1>trvl search results</h1>
      <p class="summary" id="summary">Waiting for the host to provide a tool result.</p>
    </header>
    <section class="grid" id="cards"></section>
    <pre id="raw" hidden></pre>
  </main>
  <script>
    const summary = document.getElementById("summary");
    const cards = document.getElementById("cards");
    const raw = document.getElementById("raw");

    function valueAt(obj, keys) {
      for (const key of keys) {
        if (obj && obj[key] != null && obj[key] !== "") return obj[key];
      }
      return "";
    }

    function money(item) {
      const currency = valueAt(item, ["currency", "Currency"]);
      const price = valueAt(item, ["total_price", "price", "Price", "amount"]);
      return price ? String(currency || "") + String(price) : "";
    }

    function renderItems(items, title) {
      cards.replaceChildren();
      summary.textContent = String(items.length) + " " + title;
      for (const item of items.slice(0, 24)) {
        const article = document.createElement("article");
        const h2 = document.createElement("h2");
        h2.textContent = valueAt(item, ["name", "Name", "room_name", "RoomName", "airline", "Airline"]) || title;
        article.appendChild(h2);
        const dl = document.createElement("dl");
        const fields = [
          ["Price", money(item)],
          ["Provider", valueAt(item, ["provider", "Provider", "cheapest_source"])],
          ["Type", valueAt(item, ["accommodation_type", "property_type", "cabin", "class"])],
          ["Refund", valueAt(item, ["cancellation_policy", "booking_order_hint"])],
          ["Ready", valueAt(item, ["booking_ready", "final_trip_cost_ready"])],
          ["When", valueAt(item, ["departure_time", "arrival_time", "checked_at"])]
        ];
        for (const [label, value] of fields) {
          if (value === "" || value == null) continue;
          const dt = document.createElement("dt");
          const dd = document.createElement("dd");
          dt.textContent = label;
          dd.textContent = String(value);
          dl.append(dt, dd);
        }
        const amenities = item.amenities || item.Amenities;
        if (Array.isArray(amenities) && amenities.length) {
          const dd = document.createElement("dd");
          dd.style.gridColumn = "1 / -1";
          dd.append(...amenities.slice(0, 8).map((text) => {
            const span = document.createElement("span");
            span.className = "tag";
            span.textContent = text;
            return span;
          }));
          dl.append(dd);
        }
        article.appendChild(dl);
        cards.appendChild(article);
      }
    }

    function extractStructured(result) {
      if (!result) return null;
      if (result.structuredContent) return result.structuredContent;
      if (result.data) return result.data;
      if (result.result && result.result.structuredContent) return result.result.structuredContent;
      return result;
    }

    function render(result) {
      const data = extractStructured(result);
      if (!data || typeof data !== "object") return;
      if (Array.isArray(data.offers) && data.offers.length) return renderItems(data.offers, "matched accommodation offers");
      const hotelOffers = data.hotels?.flatMap((hotel) => hotel.accommodation_offers || hotel.AccommodationOffers || []);
      if (hotelOffers && hotelOffers.length) return renderItems(hotelOffers, "matched accommodation offers");
      if (Array.isArray(data.hotels) && data.hotels.length) return renderItems(data.hotels, "hotel candidates");
      if (Array.isArray(data.flights) && data.flights.length) return renderItems(data.flights, "flight options");
      if (Array.isArray(data.rooms) && data.rooms.length) return renderItems(data.rooms, "room options");
      raw.hidden = false;
      raw.textContent = JSON.stringify(data, null, 2);
      summary.textContent = "Structured result";
    }

    window.addEventListener("message", (event) => {
      const message = event.data;
      if (message?.method === "tool/result" || message?.method === "ui/toolResult" || message?.result || message?.structuredContent || message?.data) {
        render(message.params || message);
      }
    });
  </script>
</body>
</html>`
}
