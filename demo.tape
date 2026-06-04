Output demo.gif

Set Shell "bash"
Set FontSize 14
Set Width 1100
Set Height 560
Set Theme "Catppuccin Mocha"
Set Padding 20
Set TypingSpeed 30ms
Set CursorBlink false

# This tape uses a fixture-backed one-prompt transcript so public demos do
# not fail when live travel providers throttle. Run scripts/demo/full-demo.sh
# directly to inspect the source transcript.
# It renders scripts/demo/one-prompt-demo.sh and must show:
# 1 smart MCP tool · 64 aliases · 55 CLI commands · 21 providers · No API keys

Type "scripts/demo/full-demo.sh"
Enter
Sleep 6s
