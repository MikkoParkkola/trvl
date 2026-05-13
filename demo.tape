Output demo.gif

Set Shell "bash"
Set FontSize 14
Set Width 1100
Set Height 560
Set Theme "Catppuccin Mocha"
Set Padding 20
Set TypingSpeed 30ms
Set CursorBlink false

# This tape intentionally runs real trvl commands. Do not replace with mocked
# fixtures; MIK-3440 requires current-provider demo output.

Type "# install trvl as an MCP server (dry run)"
Enter
Type "trvl mcp install --client codex --dry-run"
Enter
Sleep 1s

Type "# first travel query: nonstop HEL -> LHR weekend"
Enter
Type "trvl flights HEL LHR 2026-07-01 --return 2026-07-05 --stops nonstop --format json | jq ..."
Enter
Sleep 12s

Type "# providers: Google Flights + Skiplagged"
Enter
Type "# cheapest: CZK 7506 - Finnair AY 1337"
Enter
Type "# booking: https://www.google.com/travel/flights?q=Flights+to+London+from+Helsinki+on+2026-07-01"
Enter
Sleep 1s

Type "# 1 smart MCP tool · 63 aliases · 50 CLI commands · 21 providers · No API keys"
Enter
Sleep 2s
