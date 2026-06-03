# MCP Orchestration in trvl

## The Pattern

trvl tools are designed for **tool description-driven orchestration**. The orchestrating LLM (Claude, Cursor, etc.) reads each tool's description before calling it. When a tool needs data from an external domain — calendar events, email, tasks, health — the description tells the LLM which MCP to call first and how to pass the result in as a structured parameter.

This pattern works on every MCP client because it relies only on the LLM reading the description. No special protocol features are required. The LLM naturally chains tool calls: fetch prerequisite data, then call the trvl tool with that data as input.

## Example: find_trip_window

`find_trip_window` needs the user's calendar busy periods to avoid scheduling conflicts. Rather than fetching calendar data server-side (which would require sampling, unsupported on Claude Code), the tool description instructs the LLM:

> "Before calling this tool, use the user's calendar tool (Google Calendar, Apple Calendar, Outlook, or any other calendar MCP tool) to fetch busy intervals within the search window. Pass them as busy_intervals."

The LLM calls `google_calendar_list_events` (or equivalent), collects the intervals, then calls `find_trip_window` with `busy_intervals` populated. trvl receives structured data and does the intersection logic. No special transport features needed.

## Guidance for Future Tools

If a new trvl tool needs data from outside the travel domain, follow this pattern:

1. Accept the external data as a structured parameter (e.g. `busy_intervals`, `health_data`, `budget_envelope`).
2. In the tool description, name the type of MCP to call first and the format to pass in.
3. Do NOT implement calendar, email, task, or health logic inside trvl. Those domains belong to dedicated MCPs.
4. Document the expected parameter format precisely in the tool's `InputSchema`.

Example description snippet:
> "IMPORTANT: Before calling this tool, fetch the user's upcoming tasks from a task-management MCP (Todoist, Linear, GitHub Issues, etc.) and pass them as blocked_dates."

## Capabilities trvl Uses

| Capability | Status | Notes |
|---|---|---|
| Tools | Active | **1 advertised smart `travel` tool** (~378 tokens) routing to 64 compatibility aliases across flights, hotels, routes, hacks, trips, preferences, natural search, external-provider management — vs ~33,500 tokens if all 64 were advertised (98.9% leaner). Set `TRVL_MCP_TOOL_MODE=legacy` to advertise all 64. |
| Resources | Active | Airport codes, usage guides, price-watch subscriptions |
| Prompts | Active | `plan-trip`, `find-cheapest-dates`, `compare-hotels`, `where-should-i-go` |
| Progress Notifications | Active | Long-running searches stream `notifications/progress` |
| Dynamic Resources | Active | Trip resources created on `create_trip` |
| Resource Subscriptions | Active | Price-watch resources notify on price change |
| Structured Content | Active | Typed JSON `structuredContent` alongside text summaries |
| Output Schemas | Active | Full JSON Schema for all tool responses |

## Capabilities trvl Does NOT Use

| Capability | Reason |
|---|---|
| Sampling | Claude Code does not implement sampling client-side. The tool description pattern is the correct substitute for all cases where sampling was considered. |
| Elicitation | Modal confirm-dialog UX is wrong for search refinement — natural LLM follow-up questions give better results. Elicitation may be revisited for a future `book_trip` tool that needs explicit user confirmation before committing a booking. |
