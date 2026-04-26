#!/usr/bin/env python3
"""
Browser-based train price scraper using Playwright.

Reads JSON from stdin:
  {"provider":"trainline","from":"London","to":"Paris","date":"2026-04-10","currency":"EUR"}

Writes JSON to stdout:
  {"routes":[{"price":39.00,"currency":"GBP","departure":"06:31","arrival":"09:47",
              "duration":196,"type":"train","provider":"eurostar","transfers":0}]}

On error:
  {"routes":[],"error":"reason"}
"""

import json
import sys
import re

_Stealth = None
_STEALTH_AVAILABLE = None


def _apply_stealth(page):
    """Apply playwright-stealth patches to a page if available."""
    global _Stealth, _STEALTH_AVAILABLE
    if _STEALTH_AVAILABLE is None:
        try:
            from playwright_stealth import Stealth as _Stealth
            _STEALTH_AVAILABLE = True
        except ImportError:
            _Stealth = None
            _STEALTH_AVAILABLE = False

    if _STEALTH_AVAILABLE and _Stealth is not None:
        try:
            _Stealth().apply_stealth_sync(page)
        except Exception:
            pass


def main():
    raw = sys.stdin.read().strip()
    if not raw:
        out([], "no input on stdin")
        return

    try:
        inp = json.loads(raw)
    except json.JSONDecodeError as e:
        out([], f"invalid JSON input: {e}")
        return

    provider = inp.get("provider", "").lower()
    from_city = inp.get("from", "")
    to_city = inp.get("to", "")
    date = inp.get("date", "")
    currency = inp.get("currency", "EUR").upper()

    def load_playwright():
        try:
            from playwright.sync_api import sync_playwright
        except ImportError:
            out([], "playwright not installed: pip install playwright && playwright install chromium")
            return None
        return sync_playwright

    # Token-capture modes: only need playwright, not from/to/date.
    # Output shape differs from route scrapers — callers handle these directly.
    if provider == "sncf_key":
        sync_playwright = load_playwright()
        if sync_playwright is None:
            return
        try:
            with sync_playwright() as pw:
                key = capture_sncf_key(pw)
            print(json.dumps({"key": key or ""}), flush=True)
        except Exception as e:
            print(json.dumps({"key": "", "error": str(e)}), flush=True)
        return

    if provider == "trainline_cookie":
        sync_playwright = load_playwright()
        if sync_playwright is None:
            return
        try:
            with sync_playwright() as pw:
                cookie = capture_trainline_cookie(pw)
            print(json.dumps({"cookie": cookie or ""}), flush=True)
        except Exception as e:
            print(json.dumps({"cookie": "", "error": str(e)}), flush=True)
        return

    if not all([provider, from_city, to_city, date]):
        out([], "missing required fields: provider, from, to, date")
        return

    scrapers = {
        "trainline": scrape_trainline,
        "oebb": scrape_oebb,
        "sncf": scrape_sncf,
        "renfe": scrape_renfe,
    }

    fn = scrapers.get(provider)
    if fn is None:
        out([], f"unsupported provider: {provider}")
        return

    sync_playwright = load_playwright()
    if sync_playwright is None:
        return

    try:
        with sync_playwright() as pw:
            browser = pw.chromium.launch(
                headless=True,
                args=[
                    "--no-sandbox",
                    "--disable-blink-features=AutomationControlled",
                    "--disable-dev-shm-usage",
                ],
            )
            context = browser.new_context(
                user_agent=(
                    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) "
                    "AppleWebKit/537.36 (KHTML, like Gecko) "
                    "Chrome/131.0.0.0 Safari/537.36"
                ),
                # Override sec-ch-ua headers so they don't contain "HeadlessChrome",
                # which Datadome and other bot-detection services use as a signal.
                extra_http_headers={
                    "sec-ch-ua": '"Chromium";v="131", "Not_A Brand";v="24"',
                    "sec-ch-ua-mobile": "?0",
                    "sec-ch-ua-platform": '"macOS"',
                },
                locale="en-GB",
                viewport={"width": 1280, "height": 800},
            )
            # Mask webdriver flag and add additional browser signals to reduce bot detection.
            context.add_init_script("""
                Object.defineProperty(navigator, 'webdriver', {get: () => undefined});
                Object.defineProperty(navigator, 'plugins', {get: () => [1, 2, 3, 4, 5]});
                Object.defineProperty(navigator, 'languages', {get: () => ['en-GB', 'en']});
                window.chrome = {runtime: {}};
            """)
            page = context.new_page()
            result = fn(page, from_city, to_city, date, currency)
            browser.close()
            # Scrapers may return either a list of routes or a dict {routes, error}.
            if isinstance(result, dict):
                out(result.get("routes", []), result.get("error"))
            else:
                out(result)
    except Exception as e:
        out([], f"{provider} scraper error: {e}")


# ---------------------------------------------------------------------------
# Trainline
# ---------------------------------------------------------------------------

# Station ID map matching trainline.go
TRAINLINE_STATIONS = {
    "london": "8267",
    "paris": "4916",
    "amsterdam": "8657",
    "brussels": "5893",
    "berlin": "7527",
    "munich": "7480",
    "frankfurt": "7604",
    "hamburg": "7626",
    "cologne": "21178",
    "vienna": "22644",
    "zurich": "6401",
    "milan": "8490",
    "rome": "8544",
    "barcelona": "6617",
    "madrid": "6663",
    "prague": "17587",
    "warsaw": "10491",
    "budapest": "18819",
    "copenhagen": "17515",
    "stockholm": "38711",
    "rotterdam": "23616",
    "lille": "4652",
    "lyon": "4718",
    "marseille": "4790",
    "nice": "4836",
    "strasbourg": "153",
    "toulouse": "5306",
    "venice": "8574",
    "florence": "8434",
    "salzburg": "6994",
    "innsbruck": "10461",
    "geneva": "5335",
    "basel": "5877",
    "antwerp": "5929",
}


def capture_sncf_key(pw):
    """Navigate to sncf-connect.com homepage and capture x-bff-key from network requests.

    Launches a fresh browser, listens for any outgoing request that carries the
    x-bff-key header (injected by the SNCF SPA), and returns the key value.
    Returns an empty string if the key is not found within the timeout.
    """
    browser = pw.chromium.launch(
        headless=True,
        args=["--no-sandbox", "--disable-blink-features=AutomationControlled",
              "--disable-dev-shm-usage"],
    )
    context = browser.new_context(
        user_agent=(
            "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) "
            "AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
        ),
        extra_http_headers={
            "sec-ch-ua": '"Chromium";v="131", "Not_A Brand";v="24"',
            "sec-ch-ua-mobile": "?0",
            "sec-ch-ua-platform": '"macOS"',
        },
        locale="en-GB",
        viewport={"width": 1280, "height": 800},
    )
    context.add_init_script("""
        Object.defineProperty(navigator, 'webdriver', {get: () => undefined});
        Object.defineProperty(navigator, 'plugins', {get: () => [1, 2, 3, 4, 5]});
        Object.defineProperty(navigator, 'languages', {get: () => ['en-GB', 'en']});
        window.chrome = {runtime: {}};
    """)
    page = context.new_page()
    _apply_stealth(page)

    bff_key = {"value": ""}

    def _on_request(req):
        key = req.headers.get("x-bff-key", "")
        if key and not bff_key["value"]:
            bff_key["value"] = key

    page.on("request", _on_request)

    try:
        page.goto("https://www.sncf-connect.com/en-en",
                  wait_until="domcontentloaded", timeout=20000)
        page.wait_for_timeout(5000)
    except Exception:
        pass

    # If not found on homepage, try navigating to the search UI which triggers
    # more SPA API calls that include the key.
    if not bff_key["value"]:
        try:
            page.goto("https://www.sncf-connect.com/en-en/search",
                      wait_until="domcontentloaded", timeout=15000)
            page.wait_for_timeout(3000)
        except Exception:
            pass

    browser.close()
    return bff_key["value"]


def capture_trainline_cookie(pw):
    """Visit thetrainline.com and capture the datadome cookie value.

    Returns the cookie string in the format suitable for a Cookie header
    (e.g. "datadome=<value>"), or an empty string if not found.
    """
    browser = pw.chromium.launch(
        headless=True,
        args=["--no-sandbox", "--disable-blink-features=AutomationControlled",
              "--disable-dev-shm-usage"],
    )
    context = browser.new_context(
        user_agent=(
            "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) "
            "AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
        ),
        extra_http_headers={
            "sec-ch-ua": '"Chromium";v="131", "Not_A Brand";v="24"',
            "sec-ch-ua-mobile": "?0",
            "sec-ch-ua-platform": '"macOS"',
            "Accept-Language": "en-GB,en;q=0.9",
        },
        locale="en-GB",
        viewport={"width": 1280, "height": 800},
    )
    context.add_init_script("""
        Object.defineProperty(navigator, 'webdriver', {get: () => undefined});
        Object.defineProperty(navigator, 'plugins', {get: () => [1, 2, 3, 4, 5]});
        Object.defineProperty(navigator, 'languages', {get: () => ['en-GB', 'en']});
        window.chrome = {runtime: {}};
    """)
    page = context.new_page()
    _apply_stealth(page)

    try:
        page.goto("https://www.thetrainline.com",
                  wait_until="domcontentloaded", timeout=20000)
        # Allow Datadome challenge scripts to execute and set cookies.
        page.wait_for_timeout(4000)
        _dismiss_cookies(page)
        # Extra dwell to ensure any async cookie-setting completes.
        page.wait_for_timeout(1000)
    except Exception:
        pass

    # Extract all cookies from the browser context and find datadome.
    all_cookies = context.cookies()
    cookie_parts = []
    for c in all_cookies:
        if "thetrainline.com" in c.get("domain", ""):
            cookie_parts.append(f"{c['name']}={c['value']}")

    browser.close()

    # Return the full Cookie header string; datadome must be included.
    return "; ".join(cookie_parts)


def scrape_trainline(page, from_city, to_city, date, currency):
    from_id = TRAINLINE_STATIONS.get(from_city.lower())
    to_id = TRAINLINE_STATIONS.get(to_city.lower())
    if not from_id or not to_id:
        raise ValueError(f"no Trainline station ID for {from_city!r} or {to_city!r}")

    _apply_stealth(page)

    # Navigate directly to the RESULTS page. The SPA makes its own API calls
    # (which pass Datadome because they come from the page's own JS, not from
    # page.evaluate). The price calendar renders even when the results list
    # shows "Something went wrong".
    url = (
        f"https://www.thetrainline.com/book/results?"
        f"journeySearchType=single"
        f"&origin=urn%3Atrainline%3Ageneric%3Aloc%3A{from_id}"
        f"&destination=urn%3Atrainline%3Ageneric%3Aloc%3A{to_id}"
        f"&outwardDate={date}T08%3A00%3A00"
        f"&outwardDateType=departAfter"
        f"&passengers%5B%5D=1996-01-01"
        f"&lang=en&transportModes%5B%5D=mixed"
    )
    page.goto(url, wait_until="domcontentloaded", timeout=25000)
    _dismiss_cookies(page)
    page.wait_for_timeout(15000)  # Wait for SPA + API calls + render

    # Extract prices and times from the rendered DOM.
    result = page.evaluate(r"""JSON.stringify({
        prices: (document.body.innerText.match(/[£€$]\s*\d+[\.,]?\d*/g)||[]).slice(0,15),
        times: (document.body.innerText.match(/\d{2}:\d{2}/g)||[]).slice(0,20),
        noTickets: document.body.innerText.includes('No tickets'),
        textLen: document.body.innerText.length
    })""")
    data = json.loads(result)

    if data.get("noTickets"):
        return []  # No tickets available for this date

    prices = data.get("prices", [])
    times = data.get("times", [])

    booking_url = (
        f"https://www.thetrainline.com/book/trains/"
        f"{from_city.lower().replace(' ', '-')}/"
        f"{to_city.lower().replace(' ', '-')}/"
        f"{date}"
    )

    routes = []
    if prices:
        # Extract the minimum price from the price calendar
        min_price = None
        min_currency = currency
        for p_str in prices:
            m = re.match(r"([£€$])\s*(\d+[\.,]?\d*)", p_str)
            if m:
                symbol = m.group(1)
                amount = float(m.group(2).replace(",", "."))
                cur = {"£": "GBP", "€": "EUR", "$": "USD"}.get(symbol, currency)
                if min_price is None or amount < min_price:
                    min_price = amount
                    min_currency = cur

        if min_price and min_price > 0:
            # If we have times, pair them with the price
            if len(times) >= 2:
                # Pair departure/arrival times
                for i in range(0, min(len(times), 10), 2):
                    dep_time = times[i]
                    arr_time = times[i + 1] if i + 1 < len(times) else ""
                    routes.append({
                        "price": min_price,
                        "currency": min_currency,
                        "departure": f"{date}T{dep_time}:00",
                        "arrival": f"{date}T{arr_time}:00" if arr_time else "",
                        "duration": 0,
                        "type": "train",
                        "provider": "trainline",
                        "transfers": 0,
                        "booking_url": booking_url,
                    })
            else:
                # No individual times — just return the daily minimum price
                routes.append({
                    "price": min_price,
                    "currency": min_currency,
                    "departure": date,
                    "arrival": date,
                    "duration": 0,
                    "type": "train",
                    "provider": "trainline",
                    "transfers": 0,
                    "booking_url": booking_url,
                })

    return routes


def _parse_trainline_card(card, from_city, to_city, date, currency):
    """Legacy DOM card parser — superseded by the JS API path in scrape_trainline.

    Kept for reference. scrape_trainline now calls /api/journey-search/ directly
    from the page context after navigating to the homepage, which avoids Datadome
    detection and returns structured JSON instead of requiring DOM scraping.

    The new approach:
      1. page.goto("https://www.thetrainline.com") — establishes session cookies.
      2. page.evaluate(fetch('/api/journey-search/', ...)) — calls the internal API.
      3. Parse the JSON response directly — no DOM selectors needed.

    Retained fields for backwards-compat documentation:
      price      -> d.data.journeySearch.journeyFares[id].fares[0].price.amount / 100
      currency   -> fares[0].price.currencyCode
      departure  -> journey.departureTime (ISO-8601)
      arrival    -> journey.arrivalTime   (ISO-8601)
      duration   -> _parse_iso_duration(journey.duration)
      transfers  -> len(journey.legs) - 1
      provider   -> "trainline"
      booking_url -> https://www.thetrainline.com/book/trains/{from}/{to}/{date}
    """
    # Not called — scrape_trainline uses page.evaluate() / JS API directly.
    _ = (card, from_city, to_city, date, currency)
    return None


# ---------------------------------------------------------------------------
# ÖBB
# ---------------------------------------------------------------------------

# ÖBB shop station ExtIDs (UIC/EVA) for browser URL construction.
OEBB_SHOP_STATIONS = {
    # Austria
    "vienna": "1190100",
    "wien": "1190100",
    "salzburg": "8100002",
    "innsbruck": "8100108",
    "graz": "8100173",
    "linz": "8100013",
    # Germany
    "munich": "8000261",
    "münchen": "8000261",
    "berlin": "8011160",
    "frankfurt": "8000105",
    "hamburg": "8002549",
    # Switzerland
    "zurich": "8503000",
    "zürich": "8503000",
    "geneva": "8501008",
    "basel": "8500010",
    # Italy
    "venice": "8300137",
    "milan": "8300046",
    "rome": "8300003",
    # Hungary
    "budapest": "5500017",
    # Czech Republic
    "prague": "5400014",
    "praha": "5400014",
    # Slovakia
    "bratislava": "5600002",
    # Slovenia
    "ljubljana": "7900001",
    # Croatia
    "zagreb": "7800001",
    # Poland
    "warsaw": "5100028",
    "krakow": "5100066",
}


def scrape_oebb(page, from_city, to_city, date, currency):
    from_id = OEBB_SHOP_STATIONS.get(from_city.lower())
    to_id = OEBB_SHOP_STATIONS.get(to_city.lower())
    if not from_id or not to_id:
        raise ValueError(f"no ÖBB station ID for {from_city!r} or {to_city!r}")

    # Look up the HAFAS internal numeric IDs (subset used by the timetable API).
    # The OEBB_SHOP_STATIONS map stores EVA/UIC string IDs used for booking URLs.
    # The timetable API uses integer station numbers derived from those EVA codes.
    OEBB_HAFAS_NUMBERS = {
        "vienna": (1290401, "Wien Hbf"),
        "wien": (1290401, "Wien Hbf"),
        "salzburg": (1290301, "Salzburg Hbf"),
        "innsbruck": (1290201, "Innsbruck Hbf"),
        "graz": (1290601, "Graz Hbf"),
        "linz": (1290501, "Linz Hbf"),
        "munich": (1280401, "München Hbf"),
        "münchen": (1280401, "München Hbf"),
        "berlin": (8011160, "Berlin Hbf"),
        "frankfurt": (8000105, "Frankfurt(Main)Hbf"),
        "hamburg": (8002549, "Hamburg Hbf"),
        "zurich": (8503000, "Zürich HB"),
        "zürich": (8503000, "Zürich HB"),
        "geneva": (8501008, "Genève"),
        "basel": (8500010, "Basel SBB"),
        "venice": (8300137, "Venezia Santa Lucia"),
        "milan": (8300046, "Milano Centrale"),
        "rome": (8300003, "Roma Termini"),
        "budapest": (5500017, "Budapest-Keleti"),
        "prague": (5400014, "Praha hl.n."),
        "praha": (5400014, "Praha hl.n."),
        "bratislava": (5600002, "Bratislava hl.st."),
        "ljubljana": (7900001, "Ljubljana"),
        "zagreb": (7800001, "Zagreb Gl.kol."),
        "warsaw": (5100028, "Warszawa Centralna"),
        "krakow": (5100066, "Kraków Główny"),
    }

    from_hafas = OEBB_HAFAS_NUMBERS.get(from_city.lower())
    to_hafas = OEBB_HAFAS_NUMBERS.get(to_city.lower())
    if not from_hafas or not to_hafas:
        raise ValueError(f"no ÖBB HAFAS number for {from_city!r} or {to_city!r}")

    from_num, from_name = from_hafas
    to_num, to_name = to_hafas

    _apply_stealth(page)

    # Capture the anonymousToken the SPA fetches on page load.
    # Re-requesting it would create a new session and invalidate the existing one (HTTP 440).
    token_holder = {}
    def _capture_token(resp):
        if "/api/domain/v1/anonymousToken" in resp.url:
            try:
                d = resp.json()
                token_holder["token"] = d.get("access_token", "")
            except Exception:
                pass
    page.on("response", _capture_token)

    # Navigate to the ticket page — SPA fetches anonymousToken and sets session cookies.
    page.goto("https://shop.oebbtickets.at/en/ticket", wait_until="networkidle", timeout=25000)
    _dismiss_cookies(page)

    accesstoken = token_holder.get("token", "")
    if not accesstoken:
        raise RuntimeError("oebb: could not capture anonymousToken from page load")

    # Three-step flow using the already-established session:
    #  1. POST /api/offer/v2/travelActions  -> travelActionId
    #  2. POST /api/hafas/v4/timetable      -> connections + IDs + durations (ms)
    #  3. GET  /api/offer/v1/prices         -> price per connectionId
    result = page.evaluate(f"""
    async () => {{
        const accesstoken = {json.dumps(accesstoken)};
        const hdrs = {{
            'Content-Type': 'application/json',
            'Accept': 'application/json, text/plain, */*',
            'accesstoken': accesstoken,
            'channel': 'inet',
            'clientid': '1',
            'clientversion': '2.4.11709-TSPNEU-153089-2',
            'isoffernew': 'true',
            'lang': 'en'
        }};

        // Step 1: get travelActionId for timetable search
        const ta = await fetch('/api/offer/v2/travelActions', {{
            method: 'POST',
            headers: hdrs,
            body: JSON.stringify({{
                departureTime: true,
                from: {{number: {from_num}, name: '{from_name}'}},
                to: {{number: {to_num}, name: '{to_name}'}},
                datetime: '{date}T08:00:00.000',
                customerVias: [],
                travelActionTypes: ['timetable'],
                filter: {{productTypes: [], history: false, maxEntries: 1, channel: 'inet'}}
            }})
        }});
        if (ta.status !== 200) {{
            const t = await ta.text();
            return JSON.stringify({{error: 'travelActions HTTP ' + ta.status + ': ' + t.substring(0, 200)}});
        }}
        const taData = await ta.json();
        const travelActionId = taData.travelActions && taData.travelActions[0] && taData.travelActions[0].id;
        if (!travelActionId) {{
            return JSON.stringify({{error: 'no travelActionId: ' + JSON.stringify(taData).substring(0,200)}});
        }}

        // Step 2: fetch timetable connections
        const tt = await fetch('/api/hafas/v4/timetable', {{
            method: 'POST',
            headers: hdrs,
            body: JSON.stringify({{
                travelActionId: travelActionId,
                datetimeDeparture: '{date}T08:00:00.000',
                filter: {{regionaltrains: false, direct: false, wheelchair: false, bikes: false, trains: false, motorail: false, connections: []}},
                passengers: [{{me: false, remembered: false, markedForDeath: false, type: 'ADULT', id: 1, cards: [], relations: [], isSelected: true}}],
                count: 6,
                from: {{number: {from_num}, name: '{from_name}'}},
                to: {{number: {to_num}, name: '{to_name}'}},
                timeout: {{}}
            }})
        }});
        if (tt.status !== 200) {{
            const t = await tt.text();
            return JSON.stringify({{error: 'timetable HTTP ' + tt.status + ': ' + t.substring(0, 300)}});
        }}
        const ttData = await tt.json();
        const conns = (ttData.connections || []).slice(0, 6);
        if (conns.length === 0) {{
            return JSON.stringify({{error: 'no connections', raw: JSON.stringify(ttData).substring(0, 300)}});
        }}

        // Step 3: fetch prices for all connection IDs
        const ids = conns.map(c => c.id).filter(Boolean);
        const priceUrl = '/api/offer/v1/prices?' + ids.map(id => 'connectionIds[]=' + encodeURIComponent(id)).join('&') + '&sortType=DEPARTURE&bestPriceId=undefined';
        const pr = await fetch(priceUrl, {{headers: {{'Accept': 'application/json', 'accesstoken': accesstoken, 'channel': 'inet', 'clientid': '1', 'isoffernew': 'true'}}}});
        let priceMap = {{}};
        if (pr.status === 200) {{
            const prData = await pr.json();
            (prData.offers || []).forEach(o => {{ priceMap[o.connectionId] = o.price; }});
        }}

        return JSON.stringify({{
            count: conns.length,
            connections: conns.map(c => ({{
                id: c.id,
                dep: c.from && c.from.departure,
                arr: c.to && c.to.arrival,
                durMs: c.duration,
                sections: c.sections ? c.sections.length : 1,
                price: priceMap[c.id] || null
            }}))
        }});
    }}
    """)

    data = json.loads(result)
    if data.get("error"):
        raise RuntimeError(f"oebb api: {data['error']}")

    booking_url = (
        f"https://tickets.oebb.at/en/ticket"
        f"?stationOrigExtId={from_id}"
        f"&stationDestExtId={to_id}"
        f"&outwardDate={date}"
    )

    routes = []
    for c in data.get("connections", []):
        price = c.get("price") or 0.0
        if price <= 0:
            continue
        dep = c.get("dep") or ""
        arr = c.get("arr") or ""
        dur_ms = c.get("durMs") or 0
        sections = c.get("sections") or 1

        # Duration comes as milliseconds from the HAFAS API.
        duration = int(dur_ms) // 60000

        routes.append({
            "price": float(price),
            "currency": "EUR",
            "departure": dep,
            "arrival": arr,
            "duration": duration,
            "type": "train",
            "provider": "oebb",
            "transfers": max(0, sections - 1),
            "booking_url": booking_url,
        })

    return routes


# ---------------------------------------------------------------------------
# SNCF
# ---------------------------------------------------------------------------

SNCF_STATION_CODES = {
    "paris": "FRPAR",
    "paris gare de lyon": "FRPLY",
    "paris nord": "FRPNO",
    "paris montparnasse": "FRPMO",
    "paris est": "FRPST",
    "lyon": "FRLYS",
    "marseille": "FRMRS",
    "bordeaux": "FRBOJ",
    "toulouse": "FRTLS",
    "nice": "FRNIC",
    "strasbourg": "FRSBG",
    "lille": "FRLIL",
    "nantes": "FRNTE",
    "montpellier": "FRMPL",
    "rennes": "FRRNS",
    "avignon": "FRAVT",
    "dijon": "FRDIJ",
    "brussels": "BEBMI",
    "geneva": "CHGVA",
    "zurich": "CHZRH",
    "barcelona": "ESBCN",
    "milan": "ITMIL",
    "frankfurt": "DEFRA",
    "london": "GBSPX",
    "amsterdam": "NLASD",
    "madrid": "ESMAD",
}

# Known SNCF internal API paths to try in order.
# sncf-connect.com is a SPA; it calls a BFF (backend-for-frontend) at /bff/api/*.
_SNCF_API_PATHS = [
    ("/bff/api/v1/itinerary-search", "POST", lambda fc, tc, d: {
        "passengers": [{"type": "ADULT", "fareType": "NO_CARD"}],
        "origin": fc,
        "destination": tc,
        "date": f"{d}T06:00:00",
        "directTrainsOnly": False,
        "currency": "EUR",
    }),
    ("/bff/api/v1/trainschedules", "POST", lambda fc, tc, d: {
        "origin": fc,
        "destination": tc,
        "departureDate": f"{d}T06:00:00",
        "passengers": [{"type": "ADULT", "discountCards": []}],
        "directOnly": False,
    }),
    ("/bff/api/v1/travel-proposals", "POST", lambda fc, tc, d: {
        "origin": fc,
        "destination": tc,
        "outwardDate": d,
        "passengers": [{"type": "ADULT"}],
    }),
]


def scrape_sncf(page, from_city, to_city, date, currency):
    from_code = SNCF_STATION_CODES.get(from_city.lower())
    to_code = SNCF_STATION_CODES.get(to_city.lower())
    if not from_code or not to_code:
        raise ValueError(f"no SNCF station code for {from_city!r} or {to_city!r}")

    _apply_stealth(page)

    booking_url = (
        f"https://www.sncf-connect.com/en-en/result/train"
        f"/{from_code}/{to_code}/{date}"
    )

    # Capture the x-bff-key header that the SPA injects into all BFF requests.
    # This key is required for the BFF API to return data (401 without it).
    bff_key_holder = {}
    http_statuses = {}

    def _capture_request(req):
        bff_key = req.headers.get("x-bff-key", "")
        if bff_key:
            bff_key_holder["key"] = bff_key

    def _capture_response_status(resp):
        if resp.url.startswith("https://www.sncf-connect.com"):
            http_statuses[resp.url] = resp.status

    page.on("request", _capture_request)
    page.on("response", _capture_response_status)

    # Intercept XHR calls made by the SPA to discover the live API path/shape.
    api_responses = {}
    def _capture_api_response(resp):
        url = resp.url
        if "/bff/api/" in url or "/api/railway/" in url:
            if resp.status == 200:
                try:
                    body = resp.json()
                    api_responses[url] = body
                except Exception:
                    pass
    page.on("response", _capture_api_response)

    # Step 1: Navigate to homepage to establish Datadome session/cookies.
    # Use domcontentloaded to avoid waiting for the Datadome challenge to resolve.
    homepage_status = None
    try:
        page.goto(
            "https://www.sncf-connect.com/en-en",
            wait_until="domcontentloaded",
            timeout=25000,
        )
        page.wait_for_timeout(3000)  # Allow Datadome challenge to process
        homepage_status = http_statuses.get("https://www.sncf-connect.com/en-en")
    except Exception:
        pass

    # If the homepage returned 403 (Datadome block), we cannot proceed.
    if homepage_status == 403:
        raise RuntimeError(
            "sncf: homepage blocked by Datadome (HTTP 403). "
            "The headless browser was detected as a bot. "
            "Try running with playwright-stealth installed: pip install playwright-stealth"
        )

    _dismiss_cookies(page)

    bff_key = bff_key_holder.get("key", "")

    # Step 2: Navigate to the search results page to trigger real API calls.
    try:
        page.goto(booking_url, wait_until="domcontentloaded", timeout=30000)
        page.wait_for_timeout(4000)  # Allow SPA to finish making API calls
    except Exception:
        pass

    # If we captured any real API responses from XHR, parse them first.
    for resp_url, resp_data in api_responses.items():
        routes = _parse_sncf_response(resp_data, from_city, to_city, date, currency, booking_url)
        if routes:
            return routes

    # Step 3: Call known BFF endpoints directly from page context.
    # Use the x-bff-key if we captured it; without it most endpoints return 401.
    bff_headers_json = json.dumps({
        "Content-Type": "application/json",
        "Accept": "application/json",
        **({"x-bff-key": bff_key} if bff_key else {}),
    })

    api_errors = []
    for api_path, method, body_fn in _SNCF_API_PATHS:
        try:
            body_json = json.dumps(body_fn(from_code, to_code, date))
            result = page.evaluate(f"""
            async () => {{
                const r = await fetch('{api_path}', {{
                    method: '{method}',
                    headers: {bff_headers_json},
                    body: {json.dumps(body_json)}
                }});
                if (r.status !== 200) {{
                    const t = await r.text();
                    return JSON.stringify({{_httpError: r.status, _body: t.substring(0, 100)}});
                }}
                const d = await r.json();
                return JSON.stringify(d);
            }}
            """)
            data = json.loads(result)
            if data.get("_httpError"):
                api_errors.append(f"{api_path}: HTTP {data['_httpError']}")
                continue
            routes = _parse_sncf_response(data, from_city, to_city, date, currency, booking_url)
            if routes:
                return routes
        except Exception as e:
            api_errors.append(f"{api_path}: {e}")
            continue

    err_summary = "; ".join(api_errors[:3]) if api_errors else "no errors logged"
    raise RuntimeError(
        f"sncf: no results from API (checked {len(_SNCF_API_PATHS)} endpoints + XHR intercept, "
        f"bff_key={'present' if bff_key else 'missing'}). "
        f"Errors: {err_summary}"
    )


def _parse_sncf_response(data, from_city, to_city, date, currency, booking_url):
    """Parse any SNCF BFF/API JSON response, tolerating different response shapes."""
    routes = []

    # Try common top-level keys that contain journey arrays.
    for key in ["journeys", "proposals", "trainSchedules", "results", "trips",
                "travelProposals", "connections", "outwardJourneys"]:
        items = data.get(key, [])
        if not isinstance(items, list) or not items:
            continue
        for item in items[:8]:
            route = _extract_sncf_route(item, from_city, to_city, date, currency, booking_url)
            if route:
                routes.append(route)
        if routes:
            return routes

    # Some responses nest under a "data" key.
    if isinstance(data.get("data"), dict):
        return _parse_sncf_response(data["data"], from_city, to_city, date, currency, booking_url)

    return routes


def _extract_sncf_route(item, from_city, to_city, date, currency, booking_url):
    """Extract a single route from a SNCF journey/proposal dict."""
    # Price — try several common field names and shapes.
    price = 0.0
    cur = currency or "EUR"
    for pk in ["price", "minPrice", "cheapestPrice", "amount", "totalPrice", "priceInCents"]:
        val = item.get(pk)
        if val is None:
            continue
        if isinstance(val, dict):
            raw = val.get("amount", val.get("value", val.get("cents", 0)))
            cur = val.get("currency", val.get("currencyCode", cur))
            if isinstance(raw, (int, float)) and raw > 0:
                price = raw / 100 if "cent" in pk.lower() or "cent" in str(val).lower() else float(raw)
        elif isinstance(val, (int, float)) and val > 0:
            price = val / 100 if "cent" in pk.lower() else float(val)
        if price > 0:
            break

    if price <= 0:
        return None

    # Departure/arrival times.
    dep_time = ""
    arr_time = ""
    for dk in ["departureDate", "departureTime", "departure", "startTime", "dep",
               "scheduledDepartureTime"]:
        if item.get(dk):
            dep_time = str(item[dk])[:19]
            break
    for ak in ["arrivalDate", "arrivalTime", "arrival", "endTime", "arr",
               "scheduledArrivalTime"]:
        if item.get(ak):
            arr_time = str(item[ak])[:19]
            break

    # Duration in minutes.
    duration = 0
    for dur_key in ["duration", "travelTime", "durationInMinutes", "journeyDuration"]:
        val = item.get(dur_key)
        if isinstance(val, (int, float)) and val > 0:
            # Could be seconds, minutes, or milliseconds.
            if val > 86400:       # milliseconds
                duration = int(val) // 60000
            elif val > 1440:      # seconds
                duration = int(val) // 60
            else:                 # already minutes
                duration = int(val)
            break
        if isinstance(val, str):
            duration = _parse_iso_duration(val)
            if duration > 0:
                break

    # Transfers / changes.
    transfers = 0
    for tk in ["transfers", "changes", "numberOfChanges", "numChanges", "stops"]:
        val = item.get(tk)
        if isinstance(val, int):
            transfers = max(0, val)
            break
    # Check sections/legs count as a fallback.
    for lk in ["sections", "legs", "segments"]:
        val = item.get(lk)
        if isinstance(val, list) and len(val) > 1:
            transfers = len(val) - 1
            break

    if not dep_time:
        return None

    return {
        "price": float(price),
        "currency": cur,
        "departure": dep_time,
        "arrival": arr_time,
        "duration": duration,
        "type": "train",
        "provider": "sncf",
        "transfers": transfers,
        "booking_url": booking_url,
    }


# ---------------------------------------------------------------------------
# Renfe (Spain)  — experimental
# ---------------------------------------------------------------------------

RENFE_STATIONS = {
    # Spain
    "madrid":    "MAD",
    "barcelona": "BCN",
    "seville":   "SVQ",
    "sevilla":   "SVQ",
    "valencia":  "VLC",
    "malaga":    "AGP",
    "bilbao":    "BIO",
    "zaragoza":  "ZAZ",
    "cordoba":   "XWA",
    "alicante":  "ALC",
    "granada":   "GRX",
    "pamplona":  "PNA",
    "san sebastian": "EAS",
    "donostia":  "EAS",
    "valladolid": "VLL",
    "murcia":    "MJV",
    "palma":     "PMI",
    # International
    "paris":     "PAR",
    "marseille": "MRS",
    "lyon":      "LYS",
}

# Renfe uses numeric station codes in their API but exposes city codes on the website.
# The horarios.renfe.com endpoint uses different codes — we try the venta.renfe.com BFF.
_RENFE_STATION_NUMERIC = {
    "MAD": "60000",   # Madrid Atocha / Puerta de Atocha
    "BCN": "71801",   # Barcelona Sants
    "SVQ": "51300",   # Sevilla Santa Justa
    "VLC": "65000",   # Valencia Joaquin Sorolla
    "AGP": "61400",   # Malaga Maria Zambrano
    "BIO": "70200",   # Bilbao Abando
    "ZAZ": "65100",   # Zaragoza Delicias
    "XWA": "51600",   # Cordoba
    "ALC": "65200",   # Alicante
    "GRX": "61500",   # Granada
    "PNA": "70600",   # Pamplona Irunlarrea
    "EAS": "70100",   # San Sebastian Donostia
    "VLL": "62200",   # Valladolid Campo Grande
}


def scrape_renfe(page, from_city, to_city, date, currency):
    from_code = RENFE_STATIONS.get(from_city.lower())
    to_code = RENFE_STATIONS.get(to_city.lower())
    if not from_code or not to_code:
        raise ValueError(f"no Renfe station code for {from_city!r} or {to_city!r}")

    from_num = _RENFE_STATION_NUMERIC.get(from_code)
    to_num = _RENFE_STATION_NUMERIC.get(to_code)
    if not from_num or not to_num:
        raise ValueError(
            f"no Renfe numeric station ID for {from_city!r} (code {from_code!r}) "
            f"or {to_city!r} (code {to_code!r})"
        )

    _apply_stealth(page)

    # Navigate to venta.renfe.com to establish session cookies.
    # This is required so that CORS-protected wsrestcorp.renfe.es requests succeed.
    page.goto(
        "https://venta.renfe.com/vol/buscarTren.do?Idioma=en&Pais=UK",
        wait_until="domcontentloaded",
        timeout=25000,
    )
    page.wait_for_timeout(2000)
    _dismiss_cookies(page)

    booking_url = (
        f"https://www.renfe.com/es/en/viajar/is-ir/buscar-billetes"
        f"?origen={from_code}&destino={to_code}&fechaIda={date}&nroPasajeros=1"
    )

    # Intercept any API responses triggered during navigation (in case the SPA
    # already makes the wsrestcorp call when the component loads).
    api_responses = {}
    def _capture(resp):
        if resp.status == 200 and "wsrestcorp.renfe.es" in resp.url:
            try:
                body = resp.json()
                api_responses[resp.url] = body
            except Exception:
                pass
    page.on("response", _capture)

    # Primary: call the wsrestcorp.renfe.es price calendar API from page context.
    # This is the same endpoint called by the rf-buscador-ld web component when the
    # date picker opens.  The salesChannel must be {codApp: "VLP"} — the web
    # component passes exactly this value (discovered from p-43c1f6bd.entry.js).
    result = page.evaluate(f"""
    async () => {{
        const params = {{
            originId: {json.dumps(from_num)},
            destinyId: {json.dumps(to_num)},
            initDate: {json.dumps(date)},
            endDate: {json.dumps(date)},
            salesChannel: {{codApp: "VLP"}}
        }};
        const r = await fetch(
            'https://wsrestcorp.renfe.es/api/wsrviajeros/vhi_priceCalendar',
            {{
                method: 'POST',
                headers: {{
                    'Content-Type': 'application/json',
                    'Accept': 'application/json',
                }},
                body: JSON.stringify(params)
            }}
        );
        if (r.status !== 200) {{
            const t = await r.text();
            return JSON.stringify({{_httpError: r.status, _body: t.substring(0, 200)}});
        }}
        const d = await r.json();
        return JSON.stringify(d);
    }}
    """)
    data = json.loads(result)

    if not data.get("_httpError"):
        routes = _parse_renfe_price_calendar(data, from_city, to_city, date, currency, booking_url)
        if routes:
            return routes

    # If the primary API failed, log a useful error.
    if data.get("_httpError"):
        raise RuntimeError(
            f"renfe: wsrestcorp API returned HTTP {data['_httpError']} for "
            f"{from_city}->{to_city} on {date}. Body: {data.get('_body', '')}"
        )

    raise RuntimeError(
        f"renfe: price calendar API returned no journeys for {from_city}->{to_city} on {date}. "
        f"Response: {json.dumps(data)[:200]}"
    )


def _parse_renfe_price_calendar(data, from_city, to_city, date, currency, booking_url):
    """Parse the wsrestcorp vhi_priceCalendar response into route objects.

    The response shape:
        {
          "origin": {"name": "...", "extId": "60000"},
          "destination": {"name": "...", "extId": "71801"},
          "journeysPriceCalendar": [
            {"date": "2026-04-10", "minPriceAvailable": true, "minPrice": 36}
          ]
        }

    Since this API only returns a minimum price per day (not individual trains),
    we synthesise a single route with the cheapest available price.  Callers can
    click booking_url for full schedule details.
    """
    journeys = data.get("journeysPriceCalendar", [])
    if not journeys:
        return []

    # Find the entry for the requested date.
    entry = next((j for j in journeys if j.get("date") == date), None)
    if entry is None and journeys:
        # Fall back to the first available entry if date not found.
        entry = journeys[0]

    if entry is None:
        return []

    if not entry.get("minPriceAvailable", False):
        return []

    price = entry.get("minPrice", 0)
    if not price or price <= 0:
        return []

    return [{
        "price": float(price),
        "currency": "EUR",
        "departure": f"{date}T06:00:00",
        "arrival": "",
        "duration": 0,
        "type": "train",
        "provider": "renfe",
        "transfers": 0,
        "booking_url": booking_url,
    }]


def _parse_renfe_response(data, from_city, to_city, date, currency, booking_url):
    """Parse Renfe API JSON response."""
    routes = []
    # Renfe venta API returns a list of trains or nested under keys.
    items = data if isinstance(data, list) else None
    if items is None:
        for key in ["trenes", "trains", "servicios", "journeys", "results", "viajes"]:
            v = data.get(key, [])
            if isinstance(v, list) and v:
                items = v
                break
    if not items:
        if isinstance(data.get("data"), (dict, list)):
            return _parse_renfe_response(
                data["data"] if isinstance(data["data"], dict) else {"results": data["data"]},
                from_city, to_city, date, currency, booking_url,
            )
        return routes

    for item in items[:8]:
        if not isinstance(item, dict):
            continue
        # Price.
        price = 0.0
        cur = currency or "EUR"
        for pk in ["precioMasBarato", "precio", "price", "importe", "amount", "tarifa"]:
            val = item.get(pk)
            if isinstance(val, (int, float)) and val > 0:
                price = float(val)
                break
            if isinstance(val, dict):
                raw = val.get("importe", val.get("amount", val.get("value", 0)))
                if isinstance(raw, (int, float)) and raw > 0:
                    price = float(raw)
                    cur = val.get("moneda", val.get("currency", cur))
                    break
        if price <= 0:
            continue

        # Times.
        dep_time = ""
        arr_time = ""
        for dk in ["horaSalida", "salida", "departureTime", "departure", "horaDep"]:
            if item.get(dk):
                raw = str(item[dk])
                # Renfe times may be "HHMM" or "HH:MM" or ISO.
                if len(raw) == 4 and raw.isdigit():
                    dep_time = f"{date}T{raw[:2]}:{raw[2:]}:00"
                elif len(raw) == 5 and ":" in raw:
                    dep_time = f"{date}T{raw}:00"
                else:
                    dep_time = raw[:19]
                break
        for ak in ["horaLlegada", "llegada", "arrivalTime", "arrival", "horaArr"]:
            if item.get(ak):
                raw = str(item[ak])
                if len(raw) == 4 and raw.isdigit():
                    arr_time = f"{date}T{raw[:2]}:{raw[2:]}:00"
                elif len(raw) == 5 and ":" in raw:
                    arr_time = f"{date}T{raw}:00"
                else:
                    arr_time = raw[:19]
                break

        if not dep_time:
            continue

        duration = 0
        for dur_key in ["duracion", "duration", "travelTime"]:
            val = item.get(dur_key)
            if isinstance(val, (int, float)) and val > 0:
                duration = int(val) if val <= 1440 else int(val) // 60
                break
            if isinstance(val, str):
                duration = _parse_iso_duration(val)
                if not duration:
                    # Format "HH:MM" or "H:MM"
                    parts = val.split(":")
                    if len(parts) == 2:
                        try:
                            duration = int(parts[0]) * 60 + int(parts[1])
                        except ValueError:
                            pass
                if duration:
                    break

        transfers = 0
        for tk in ["transbordos", "cambios", "transfers", "changes"]:
            val = item.get(tk)
            if isinstance(val, int):
                transfers = max(0, val)
                break

        routes.append({
            "price": price,
            "currency": cur,
            "departure": dep_time,
            "arrival": arr_time,
            "duration": duration,
            "type": "train",
            "provider": "renfe",
            "transfers": transfers,
            "booking_url": booking_url,
        })

    return routes


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _parse_iso_duration(s):
    """Parse ISO 8601 duration string (PT2H15M, PT45M, P0DT3H) into minutes."""
    if not s:
        return 0
    m = re.search(r"(?:(\d+)H)?(?:(\d+)M)?", s)
    if m:
        hours = int(m.group(1) or 0)
        mins = int(m.group(2) or 0)
        return hours * 60 + mins
    return 0


def _dismiss_cookies(page):
    """Accept cookie banners — try common button patterns."""
    selectors = [
        "button[id*='accept']",
        "button[id*='cookie']",
        "button[class*='accept']",
        "button[class*='cookie']",
        "[data-testid*='cookie'] button",
        "#onetrust-accept-btn-handler",
        ".cookie-accept",
        "button:has-text('Accept all')",
        "button:has-text('Accept cookies')",
        "button:has-text('I agree')",
        "button:has-text('Agree')",
        "button:has-text('OK')",
    ]
    for sel in selectors:
        try:
            btn = page.query_selector(sel)
            if btn and btn.is_visible():
                btn.click()
                page.wait_for_timeout(500)
                return
        except Exception:
            continue


def out(routes, error=None):
    payload = {"routes": routes}
    if error:
        payload["error"] = error
    print(json.dumps(payload), flush=True)


if __name__ == "__main__":
    main()
