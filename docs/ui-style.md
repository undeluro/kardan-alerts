# Kardan UI style

Kardan is an operations console for people triaging incoming events and configuring alert rules. Its compact panels, graphite surfaces, and coordinated status colors draw on monitoring dashboards. Keep the current status, the evidence behind it, and the next available action easy to find.

## Layout and hierarchy

- Keep **Alerts**, **Rules**, **Schemas**, **Sources**, **Runbooks**, **Markets**, **Deliveries**, **Teams**, and **Audit** visible in workspace navigation. The desktop rail becomes a horizontally scrollable row on smaller screens.
- Give each screen one H1 and one obvious primary action when the screen calls for one. Put workspace controls in the top bar and move task-specific controls next to the results they change.
- Lead the home screen with a compact strip of current counts, followed by the alert queue. Show title and source alongside severity, status, event count, and recency. Open the detail sheet to see the remaining fields, deliveries, and original event.
- Treat alert state, severity, and delivery state as separate concepts. Pair color with explicit labels. Use real counts, and display a dash when a metric is unavailable.
- Use a visible filter group and show the number of loaded records. Keep loading, no matches, and errors distinguishable. Preserve useful results when a background refresh fails.

## Visual tokens

| Role | Light | Dark |
| --- | --- | --- |
| Workspace | `#F2F4F6` | `#101317` |
| Panel | `#FFFFFF` | `#181D22` |
| Control | `#F7F8FA` | `#12161B` |
| Text | `#202830` | `#ECF0F3` |
| Secondary text | `#555F6B` | `#A8B1BD` |
| Divider | `#D8DDE3` | `#333B45` |
| Accent | `#A04A11` | `#FFB765` |
| Navigation rail | `#E9EDF1` | `#15191E` |

Use the CSS variables in `web/src/index.css` for console surfaces. Use a compact sans-serif stack for UI and monospace with tabular numerals for payloads, identifiers, and metric values. The amber accent marks the selected view, primary actions, and keyboard focus. Keep alert-severity and delivery-state colors paired with text labels.

Use red for the open-alert counter, blue for acknowledged alerts, green for matching event volume, and amber for pending deliveries. The counters show current values, with their time window in the label where applicable. Add time-series charts when the underlying data includes a time series.

Prefer 6 px panel corners, 4 px control corners, and 8–12 px gaps. Dark mode is the default. The toolbar theme toggle remembers the user's choice. Honor reduced motion and forced colors. Let long titles, identifiers, and error text wrap or scroll within their panels.

## Components and interactions

- Reuse `Button`, `Badge`, `PageHeader`, `Card`, and `Notice` from `web/src/ui.tsx`. Use [Lucide React](https://lucide.dev/guide/react/) for icons, with a label on icon-only controls and hidden decorative icons.
- Use [Radix Dialog](https://www.radix-ui.com/primitives/docs/components/dialog) for routed sheets. Give the sheet a title, keep actions visible below scrollable content, and return focus to the originating alert or queue heading.
- Use links for navigation and buttons for state changes. Use the shared `Select` for native dropdown behavior with an inset Lucide chevron. Include accessible labels for filters and use `aria-pressed` on view toggles.
- Use `ChannelIcon` and `channelLabel` for email, SMS, phone, and in-app delivery surfaces.
- Use active verbs for actions such as **Acknowledge**, **Resolve alert**, and **Create rule**. Keep the rule test next to the editor, explain that testing sends no notifications, and label the result separately from saved rule state.
- For runbooks, show the next available action and its required approval in the alert sheet. Use a timestamped timeline for completed steps and results.
