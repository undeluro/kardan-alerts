# Kardan UI style

Kardan is an operations console for people triaging incoming events and configuring alert rules. Keep the current status, the evidence behind it, and the next available action easy to find.

## Layout and hierarchy

- Keep **Alerts**, **Rules**, **Schemas**, **Sources**, **Deliveries**, and **Teams** visible in workspace navigation. The desktop rail becomes a horizontally scrollable row on smaller screens.
- Give each screen one H1 and one obvious primary action when the screen calls for one. Put workspace controls in the top bar and move task-specific controls next to the results they change.
- Put the alert queue first on the home screen. Show title and source alongside severity, status, event count, and recency. Open the detail sheet to see the remaining fields, deliveries, and original event.
- Treat alert state, severity, and delivery state as separate concepts. Pair color with explicit labels. Use real counts, and display a dash when a metric is unavailable.
- Use a visible filter group and show the number of loaded records. Keep loading, no matches, and errors distinguishable. Preserve useful results when a background refresh fails.

## Visual tokens

| Role | Light | Dark |
| --- | --- | --- |
| Workspace | `#F4F6FA` | `#111827` |
| Surface | `#FFFFFF` | `#182235` |
| Text | `#17243A` | `#F0F4FB` |
| Secondary text | `#536278` | `#AEBBD0` |
| Divider | `#DCE3ED` | `#354259` |
| Accent | `#2554B8` | `#9BBCFF` |
| Navigation rail | `#111E33` | `#0B1424` |

Use the CSS variables in `web/src/index.css` for new console surfaces. Use the system sans-serif stack for UI, monospace for payloads and identifiers, and tabular numerals for counts. Use the accent for navigation, primary actions, and focus. Keep green for success or a live connection, amber for warnings and retrying, and red for critical or failed states. Retain rule-configured presentation colors inside the alert UI.

Prefer 12 px panel corners, 6–8 px control corners, and 12–16 px gaps in dense views. Let long titles, identifiers, and error text wrap or scroll within their panels. Respect the OS theme and reduced-motion preference.

## Components and interactions

- Reuse `Button`, `Badge`, `PageHeader`, `Card`, and `Notice` from `web/src/ui.tsx`. Use [Lucide React](https://lucide.dev/guide/react/) for icons, with a label on icon-only controls and hidden decorative icons.
- Use [Radix Dialog](https://www.radix-ui.com/primitives/docs/components/dialog) for routed sheets. Give the sheet a title, keep actions visible below scrollable content, and return focus to the originating alert or queue heading.
- Use links for navigation and buttons for state changes. Use native, labeled inputs for filters. Make the selected state available to keyboard and screen-reader users.
- Use active verbs for actions such as **Acknowledge**, **Resolve alert**, and **Create rule**. Keep the rule test next to the editor, explain that testing sends no notifications, and label the result separately from saved rule state.
- For future runbooks, show the next available action and its required approval in the alert sheet. Use a timestamped timeline for completed steps and results.
