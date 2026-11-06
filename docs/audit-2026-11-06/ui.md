## bitploy dashboard UX audit

I walked the dashboard against the mock API as the beginner, the power user and the admin, at 1440x900, 1280x800 and 390x844, in light and dark mode. The worst problems: the Ready-made Add dialog can silently break a working database connection, a public database still says "Private", the Postgres Storage tab can switch off its own data, and the compose flow lets you deploy over your limit. The vite server is stopped, no tracked files changed (`git status` shows only `.claude/`), and all 87 screenshots are in `/tmp/playwright-mcp/ux-audit/`.

**Mock gaps, not product bugs:**
- `GET /events` returns 404, so every page polls and shows "Could not follow live".
- `PATCH /admin/users/:id` and `POST /apps/:id/domains` are missing, so saving limits and adding a domain end in a red "The mock does not know..." toast.
- No mock app uses MariaDB or MySQL, so I couldn't check those Database tabs.
- The mock's numbers disagree with each other. Ada shows 17/12 apps and 4.8/4.0 GB on Projects but 18/30 and 5.1/8.0 GB under Admin &gt; People, and Server and All apps disagree on reserved memory. I left these out of the findings.

### Top 5 to fix first
1. Ready-made Add to a project that already has Postgres silently overwrites the main app's working `DATABASE_URL` (#1).
2. A public port on a database still says "Private" in the header and on the card (#5).
3. The Postgres Storage tab shows the wrong path, an empty file browser and a switch that can drop the database's data (#9).
4. The compose paste lets you deploy over your limit, while the Add dialog blocks the same case (#3).
5. Two switches (password protection, public port) turn orange while the label still says Off (#16).

---

### Ready-made / Add database dialog
1. **High.** Steps: Ready-made &gt; PostgreSQL &gt; Add to Shop, which already has `postgres`. The dialog renames the new one to `postgres-2` without saying so and ticks "web" to connect. web already has `DATABASE_URL=${{postgres.DATABASE_URL}}`, so adding would point the live shop at an empty database. Hurts beginners, who will lose their data connection. Fix: when the project already has the same kind of database, untick connect and warn "web already uses postgres; connecting replaces DATABASE_URL". Name the rename ("Named postgres-2 because postgres exists"). `/tmp/playwright-mcp/ux-audit/19-add-postgres-shop.png`
2. **Low.** The "reach it at | postgres-2:5432 | ; the internet cannot." sentence breaks into columns. The raw `${{postgres.DATABASE_URL}}` syntax is shown to beginners. The project order differs from the sidebar. Fix: keep the address inline, show "DATABASE_URL → postgres-2" and use the sidebar order. `/tmp/playwright-mcp/ux-audit/17-*.png`, `/tmp/playwright-mcp/ux-audit/19-add-postgres-shop.png`

### New project (/new)
3. **High.** Steps: paste compose while over the limit. The red text "needs 768 MB of the 0 MB you have left" shows, but Deploy stays enabled. The Add PostgreSQL dialog disables Add in the same situation, so the two screens disagree. Hurts beginners, who get a failed deploy. Fix: use one rule in both places (disable, or say "deploy will fail: over your memory limit"). `/tmp/playwright-mcp/ux-audit/14-compose-preview-bottom.png`, `/tmp/playwright-mcp/ux-audit/18-add-postgres-bottom.png`
4. **Medium.** A compose service with `build: .` previews as "Built from your code", while the hint above says every service needs a published image. Fix: mark that service red in the preview: "needs `image:`, `build:` is not supported here". `/tmp/playwright-mcp/ux-audit/13-compose-preview.png`

Further /new findings are #26–#28.

### Canvas and app header
5. **High.** Steps: open a public port on shop-postgres (Anyone, 1 hour). The card reads "Private, at postgres:5432 [public]" and the app header still says "Private" with no badge. Hurts power users, who don't notice their database is open to the internet. Fix: replace "Private" with "Public at host:30001, closes in 57 min" on both the card and the header. `/tmp/playwright-mcp/ux-audit/62-canvas-port-open.png`, `/tmp/playwright-mcp/ux-audit/61-header-after-port-open.png`
6. **Medium.** The quick-look panel for postgres lists "More: Deployments, Logs, Variables, Storage, Settings" with no Database link. The compose app's `db` service has no Database tab at all. Yet "Add to Notes stack" suggests adding PostgreSQL even though the compose file already has one. Fix: add a Database link for database apps and services, and don't suggest a database the stack already has. `/tmp/playwright-mcp/ux-audit/22-quicklook-postgres.png`, `/tmp/playwright-mcp/ux-audit/50-*.png`, `/tmp/playwright-mcp/ux-audit/52-*.png`
7. **Low.** Wording differs between screens:
   - "Talks to / Used by" on the cards vs "uses db, cache" in the compose card
   - "Open app" (header) vs "Open website" (panel)
   - "Restart" vs "Restart all"
   - "web Website" vs "Website web"

   Fix: pick one term for each. `/tmp/playwright-mcp/ux-audit/81-dark-logs.png`, `/tmp/playwright-mcp/ux-audit/91-1280-notes-panel.png`
8. **Low.** At 1280 wide the quick-look panel covers the selected card, cutting off its right edge. The version label is unclear: "PostgreSQL 18" in the header, "18.4" in the Database tab, plus a "18.6 available" pill. The Valkey cache tooltip says "backed up", but caches have no backups. `/tmp/playwright-mcp/ux-audit/91-1280-notes-panel.png`, `/tmp/playwright-mcp/ux-audit/92-1280-db.png`, `/tmp/playwright-mcp/ux-audit/87-m-db.png`

### Postgres app: Storage / Overview / Settings
9. **High.** The Postgres Storage tab shows a "Keep saved files /data" switch, which is the wrong path (Postgres keeps data in /var/lib/postgresql/data) and can be turned off on a database. It also shows "Browse saved files /data: This folder is empty" with Upload and New folder buttons while 184 MB is in use, plus "Open a SQLite database to browse it". Hurts beginners: they can delete their database. Fix: for database templates, hide the switch and the file browser, show the size, and link to Backups and the Database tab. `/tmp/playwright-mcp/ux-audit/42-postgres-storage.png`
10. **Low.** The Overview "Saved files" label is hard-coded to `/data` (AppOverviewTab.vue:218/227). Postgres Settings first shows the web-app form (Build type, Address) before switching to the image form. The delete text says "its address" for a database. Fix: use the template's mount path, show a skeleton until the app kind is known, and use database wording. `/tmp/playwright-mcp/ux-audit/23-*.png`, `/tmp/playwright-mcp/ux-audit/47-settings-loading-flash.png`, `/tmp/playwright-mcp/ux-audit/46-*.png`
11. **Low.** On Overview, "Live version" says "Source: Rollback to an earlier version" while the line beside it says "Deployed by a GitHub push". `/tmp/playwright-mcp/ux-audit/85-m-app-overview.png`

### Database tab (commit a910db6)
12. **Medium.** There are two write controls with opposite polarity. The header "Read-only" switch is off by default, so cell editing is on. The SQL tab has an "Allow writes" switch that is also off. Editing a production database by default is risky for beginners. Fix: one control, read-only by default, named "Allow changes" in both places. `/tmp/playwright-mcp/ux-audit/24-*.png`, `/tmp/playwright-mcp/ux-audit/25-*.png`
13. **Low.** There are two Refresh buttons (header and grid). Postgres has no export, while the SQLite viewer has Download. The page title drops the app name after switching subtabs ("Database - bitploy"). The Database tab vanishes from the tab bar while loading. On mobile it says "Double-click a cell to edit", which doesn't work on touch. `/tmp/playwright-mcp/ux-audit/87-m-db.png`, `/tmp/playwright-mcp/ux-audit/24-*.png`

### Delete flows
14. **Medium.** Deleting a database that is in use ("postgres is in use") is a dead end. Delete is disabled and you must open each app's variables yourself. Fix: list the variables and offer "Remove these variables and delete". `/tmp/playwright-mcp/ux-audit/45-delete-used-db.png`
15. **Low.** The project delete doesn't mention downloading backups, while app delete does. Project Settings is narrower than the header. `/tmp/playwright-mcp/ux-audit/48-*.png`, `/tmp/playwright-mcp/ux-audit/49-*.png`

### Settings tab: password and public port
16. **Medium.** The Password protection and Public port switches turn orange while the label still says "Off ... until you click Turn on / Open port". It is a double control with a misleading state. Fix: the switch only reveals the form, and the label reads "Not on yet: fill in and click Turn on"; or drop the switch and use a single button. `/tmp/playwright-mcp/ux-audit/41-password-protect.png`, `/tmp/playwright-mcp/ux-audit/59-public-port.png`
17. **Medium.** "Who can connect" on a public port defaults to Anyone. Fix: default to "Only my IP". `/tmp/playwright-mcp/ux-audit/59-public-port.png`

### Logs and failed deploys (commit 7b25bf3)
18. **Medium.** Highlighting uses a lot of blue: INFO, paths, `file://` and `https://` links, stack-frame paths, YAML keys. GET is purple. This is clear in dark mode and goes against the no-blue taste. Fix: move `--code-key` and link colours to neutral grey or the orange family, and keep red and yellow for levels. `/tmp/playwright-mcp/ux-audit/81-dark-logs.png`, `/tmp/playwright-mcp/ux-audit/32-web-logs.png`, `/tmp/playwright-mcp/ux-audit/13-compose-preview.png`
19. **Medium.** The failed deploy summary is generic: "The build failed. The last log lines say which step went wrong." Only Docker's "exit code 1" line is highlighted. The real cause ("formatPrice is not exported", "error during build") is not. Hurts beginners. Fix: pull the first `error` line above the Docker wrapper and show it as the summary. `/tmp/playwright-mcp/ux-audit/34-failed-deploy-expanded.png`

### Deployments / rollback
20. **Low.** The list says "Rollback to an earlier version" but the button says "Go back to this version". The confirm doesn't say that the next GitHub push deploys again. `/tmp/playwright-mcp/ux-audit/35-*.png`, `/tmp/playwright-mcp/ux-audit/36-*.png`

### Variables
21. **Low.** The API_URL chip shows "api . HOST" and hides the `http://...:8080` around it. A saved secret shows its first two letters ("ne••••") although the page says values are hidden. "Restart needed" is reported three times (toast, banner, dot). `/tmp/playwright-mcp/ux-audit/29-*.png`, `/tmp/playwright-mcp/ux-audit/31-*.png`

### Traffic (commit 4e8acde)
22. **Medium.** The "Speed" card shows a time (0.09 s), so a red "↑7%" reads oddly; "Answer time" with "slower 7%" would be clearer. The selected metric card's grey fill looks disabled. "Slowest 1 in 20" is drawn in green. For the error rate, "↓1%" could mean relative change or percentage points. `/tmp/playwright-mcp/ux-audit/53-*.png`, `/tmp/playwright-mcp/ux-audit/56-*.png`

### Backups
23. **Low.** The schedule is in UTC ("03:00 UTC") while backup times are in local time. The "Keep [the latest] [7] backups" select reads awkwardly. The Dump and Files kinds are not explained. `/tmp/playwright-mcp/ux-audit/43-*.png`

### Projects page
24. **Medium.** The stat cards show "4.8 GB of 4.0 GB reserved" and "17 of 12 allowed" in red with no explanation or next step, even in the first-run empty state. "Reserved" is jargon. Also, ProjectsPage.vue:77-79 ignores `quota.overallocate`, so a user who is allowed to overallocate still sees red over-limit cards (commit 630cb7e). Fix: hide the cards when empty, show "No limit on memory and CPU" when overallocating, and add "Ask your admin for more" when over. `/tmp/playwright-mcp/ux-audit/01-*.png`, `/tmp/playwright-mcp/ux-audit/04-*.png`
25. **Low.** The empty-state source cards ("Dockerfile or compose: Paste one, or a Docker image name") differ from the /new cards, which have a separate Docker image card. The breadcrumb reads "Projects &gt; Ready-made" although Ready-made is a sibling page. `/tmp/playwright-mcp/ux-audit/04-*.png`, `/tmp/playwright-mcp/ux-audit/15-new-image.png`

### New project: names and navigation
26. **Medium.** Steps: pick a GitHub repo whose name is taken. You only learn "The address portfolio is taken" after pressing Deploy, and the sidebar then has both "portfolio" and "Portfolio". Fix: check the address as you type and suggest `portfolio-2`. `/tmp/playwright-mcp/ux-audit/09-after-deploy.png`, `/tmp/playwright-mcp/ux-audit/11-*.png`
27. **Medium.** The chosen source is not in the URL, so browser Back leaves the flow instead of returning to the source picker. Fix: add `?source=` to the URL for each step. `/tmp/playwright-mcp/ux-audit/15-new-image.png`
28. **Low.** Disabled orange buttons ("Save name", "Save limit") are pale, with low contrast.

### Login
29. **Low.** "a@b.c" is rejected as not an email. The heading "Log in" plus "Log in to see your projects." says the same thing twice. `/tmp/playwright-mcp/ux-audit/02-*.png`

### Command palette (Ctrl K)
30. **Medium.** Typing "restart" or "deploy" gives "Nothing matches": the palette has no actions. While you type, groups are added twice: Vue warns "Duplicate keys: Shop / Projects / Apps / Go to" and stale groups show. "logs" lists every app › Logs with no ranking. Fix: give groups unique keys (project id plus group), add actions for the current app (Restart, Deploy latest, Open logs), and rank the current project first. `/tmp/playwright-mcp/ux-audit/63-palette.png`, `/tmp/playwright-mcp/ux-audit/64-palette-search.png`, `/tmp/playwright-mcp/ux-audit/65-palette-restart.png`
31. **Low.** After Esc closes the palette, focus goes to `&lt;body&gt;` rather than where it was. Fix: save `document.activeElement` on open and restore it on close.

### Accessibility, all screens
32. **Low.** The page has two nested `&lt;main&gt;` landmarks. Fix: make the inner one a `&lt;div&gt;` or `&lt;section&gt;`.

### Admin: People and Limits (commit 630cb7e)
33. **Medium.** In the Limits dialog, turning on "Allow overallocating memory and CPU" leaves the Memory and CPU fields editable, though they are now ignored. After saving, the People table still shows "864 MB / 4.0 GB" with no sign of overallocation. Hurts admins, who can't see who is exempt. Fix: grey out the Memory and CPU fields when the switch is on ("not checked"), and show "No limit" or an "Overallocating" badge in the Memory column. `/tmp/playwright-mcp/ux-audit/72-admin-overalloc-on.png`, `/tmp/playwright-mcp/ux-audit/73-admin-after-save.png`, `/tmp/playwright-mcp/ux-audit/70-admin.png`
34. **Low.** The Limits dialog asks for memory and storage in MB (4096, 20480) while every table shows GB. "CPU in total: 4" has no unit, though elsewhere the UI says "1 CPU is one processor core". The domains column uses "Default + 1 domain", which is jargon. Fix: accept GB and label the field "CPU cores". `/tmp/playwright-mcp/ux-audit/71-admin-limits.png`

### Admin: other tabs
35. **Low.** The "Database" tab goes to a separate route, `/admin/database`, so the admin tab bar disappears and the tab looks broken. Fix: render it inside the tab bar, or style it as a link. `/tmp/playwright-mcp/ux-audit/80-admin-database.png`
36. **Low.** The Activity log shows raw keys ("deployment.queue", "env.set") and "role: user", while the invite form says "Member". The Domains tab says "None, fine for Full". Fix: hide the keys behind a hover, say "Member", and write "No certificate needed for Full". `/tmp/playwright-mcp/ux-audit/79-admin-activity-log.png`, `/tmp/playwright-mcp/ux-audit/77-admin-domains.png`

### Mobile (390x844)
37. **Medium.** On an app page, the "..." button wraps alone onto a second row. The tab bar is clipped ("Va") with no scroll hint. On postgres/database the active "Database" tab is scrolled out of view, so you can't tell where you are. Fix: scroll the active tab into view, fade the edge, and put "..." on the same row. `/tmp/playwright-mcp/ux-audit/85-m-app-overview.png`, `/tmp/playwright-mcp/ux-audit/87-m-db.png`
38. **Low.** The admin tabs show a visible horizontal scrollbar. "MEMORY NOW 163 MB of 512 MB" wraps "MB" onto its own line. No page overflows sideways. The canvas, the Traffic tab and the admin table work on mobile. `/tmp/playwright-mcp/ux-audit/89-m-admin.png`, `/tmp/playwright-mcp/ux-audit/85-m-app-overview.png`

**What works well:**
- Esc closes the quick-look panel and cleans the URL.
- The restore confirm text is clear, and the domain DNS steps are concrete.
- Deleting a project asks you to type its name.
- Dark mode on the canvas looks right (`/tmp/playwright-mcp/ux-audit/82-dark-canvas.png`).
- The Database tab shows a skeleton while loading.
