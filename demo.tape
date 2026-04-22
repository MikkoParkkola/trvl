Output demo.gif

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 600
Set Theme "Catppuccin Mocha"
Set Padding 20
Set TypingSpeed 30ms
Set CursorBlink false

# Hidden setup: mock trvl function dispatches to pre-rendered output files
Hide
Type `trvl() { case "$1" in prefs) cat .demo/prefs.txt;; discover) cat .demo/discover2.txt;; when) cat .demo/when2.txt;; multi-city) cat .demo/multicity2.txt;; ground) cat .demo/ground2.txt;; esac; }`
Enter
Type "export PS1='$ '"
Enter
Type "clear"
Enter
Show

# ═══ ACT 1: "It knows me" — preferences set the stage ═══

Type "# trvl knows me. No hostels. My neighborhoods. My airline status."
Enter
Sleep 300ms
Type "trvl prefs"
Enter
Sleep 3s

# ═══ ACT 2: "Where should I go?" — budget discovery ═══

Type "# EUR 500, free week in June. Surprise me."
Enter
Sleep 300ms
Type "trvl discover --budget 500 --from 2026-06-01 --until 2026-06-30"
Enter
Sleep 4s

# ═══ ACT 3: "When can I go?" — calendar-aware planning ═══

Type "# Krakow looks great. When? Skip the 20-22nd."
Enter
Sleep 300ms
Type "trvl when --to KRK --from 2026-06-01 --until 2026-06-30 --busy 2026-06-20:2026-06-22"
Enter
Sleep 3s

# ═══ ACT 4: "I want 3 cities" — multi-city optimization ═══

Type "# Actually, Prague and Madrid too. Cheapest order?"
Enter
Sleep 300ms
Type "trvl multi-city HEL --visit PRG,KRK,MAD --dates 2026-07-01,2026-07-21"
Enter
Sleep 3s

# ═══ ACT 5: "How do I get there?" — ground transport ═══

Type "# Side trip to Tallinn. Ferry or bus?"
Enter
Sleep 300ms
Type "trvl ground HEL TLL --date 2026-06-16"
Enter
Sleep 3s

# ═══ CLOSING ═══

Sleep 300ms
Type "# 57 MCP tools · 42 CLI commands · 21 providers · No API keys"
Enter
Sleep 300ms
Type "# brew install MikkoParkkola/tap/trvl"
Enter
Sleep 2s
