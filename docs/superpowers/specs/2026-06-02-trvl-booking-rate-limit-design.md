# trvl: Booking.com Integration + Rate Limit Management

**Data:** 2026-06-02
**Status:** Design approvato

## Obiettivo

Rendere trvl un travel agent affidabile integrando Booking.com come fonte dati primaria (non fallback) e gestendo il rate limiting di Google Hotels in modo trasparente per l'utente.

## Architettura

```
CLI/MCP → HotelSearchEngine → [Google Hotels, Booking Search] → MergeHotelResults
         → RoomEngine → [Google Entity, Booking Detail] → mergeRoomTypes
         → RateManager → backoff adattivo + warning utente
```

## Componenti

### 1. Booking Search (`booking_search.go`) 🆕

Cerca hotel su Booking.com per località+date, parallelamente a Google Hotels.

- **Input:** `location, checkIn, checkOut, currency, maxPrice, stars`
- **Output:** `[]HotelResult` con nome, prezzo, rating, BookingURL
- **Metodo:** Scraping HTML della search page Booking.com, estrazione da `window.INITIAL_STATE` o JSON-LD embedded
- **Rate limit:** 1 req/3s (Booking è aggressivo)
- **Degradazione:** se parsing fallisce → warn, zero risultati, non blocca

### 2. Booking Rooms (`booking_rooms.go`) 🔧

Già esistente (`FetchBookingRooms`). Modifiche:

- Attivato SEMPRE per ogni hotel che ha un Booking URL nei Sources
- **Nota importante:** Solo gli hotel trovati da `SearchBooking` avranno un Booking.com URL.
  Gli hotel che arrivano SOLO da Google Hotels (nessun match su Booking) non avranno
  Booking URL e useranno solo Google entity page per le camere.
- `SearchBooking` restituisce ogni hotel con `BookingURL` già popolato
- Dopo il merge (`MergeHotelResults`), se l'hotel merged ha BookingURL → usalo
- Se l'hotel merged ha solo URL Google → salta Booking rooms, usa solo Google
- Eseguito IN PARALLELO a Google entity page (goroutine)
- Risultati fusi con `mergeRoomTypes` (già esistente)

### 3. Rate Manager (`ratelimit.go`) 🆕

Gestisce rate limiting per tutti i provider.

- Token bucket: Google 2 req/s, Booking 1 req/3s
- Backoff adattivo: 2s → 4s → 8s su 429 consecutivi
- Threshold warning: dopo 2+ 429, mostra guida utente
- Cache: 10 min default, 30 min se rate-limited
- Health check: esposto via `trvl provider-health`

### 4. Integrazione in `searchHotelsCore` 🔧

Booking search aggiunto come goroutine parallela a Trivago/HomeToGo:

```go
auxWg.Add(1)
go func() {
    defer auxWg.Done()
    res, err := SearchBooking(ctx, location, auxOpts)
    if err != nil { slog.Warn("booking search failed", ...); return }
    bookingResults = res
}()
```

### 5. Integrazione in `GetRoomAvailabilityWithOpts` 🔧

FetchBookingRooms lanciato in parallelo a tryEntityPage:

```go
var bookingRooms []RoomType
var bookingWg sync.WaitGroup
if bookingURL != "" {
    bookingWg.Add(1)
    go func() {
        defer bookingWg.Done()
        bookingRooms, _ = FetchBookingRooms(ctx, bookingURL, checkIn, checkOut, currency)
    }()
}
// ... entity page ...
bookingWg.Wait()
rooms = mergeRoomTypes(rooms, bookingRooms)
```

## Test (TDD)

Ogni file nuovo ha il suo test file prima dell'implementazione.

### booking_search_test.go
- `TestSearchBooking_ReturnsHotels`: HTML Booking mockato con 3 hotel
- `TestSearchBooking_EmptyResults`: pagina senza risultati
- `TestSearchBooking_RateLimited`: simula 429, verifica warn log
- `TestSearchBooking_ParseFailure`: HTML malformato, zero risultati

### ratelimit_test.go
- `TestRateManager_BackoffIncreases`: 3 richieste, backoff 2→4→8s
- `TestRateManager_ThresholdWarning`: 2+ 429, warning emesso
- `TestRateManager_CacheHit`: stessa richiesta entro 10 min → cache

### booking_rooms_test.go (aggiunte)
- `TestFetchBookingRooms_ParallelWithGoogle`: verifica goroutine parallela
- `TestMergeRoomTypes_BookingWins`: Booking più economico di Google

## Rischi e Mitigazioni

| Rischio | Probabilità | Mitigazione |
|---------|------------|-------------|
| Booking cambia layout search | Media | Degradazione graceful, test di regressione |
| Booking rate-limit aggressivo | Alta | 1 req/3s, backoff, circuit breaker |
| Google blocca definitivamente | Bassa | Google cambia raramente batchexecute |
| Troppe richieste in parallelo | Media | RateManager centralizzato |

## Guida Utente

Al primo rate limit o su `trvl provider-health`:
```
💡 Per evitare rate limiting:
   • Aspetta 10s tra ricerche consecutive
   • Usa intervalli di date ampi
   • `trvl provider-health` mostra lo stato
   • Dopo un 429, attendi 60s
```
