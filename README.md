# revu

LAN-local review app for Markdown, HTML, text, and file review threads.

revu starts a local web server, stores review data on the host machine, and lets reviewers on the same network comment while looking at the preview in the same app.

## Build and Run

Build and run:

```bash
go build -o dist/revu ./cmd/revu
./dist/revu
```

By default revu listens on `0.0.0.0:8080`. The startup log prints a local URL and one or more LAN URLs. Share the LAN URL with reviewers on the same network.

Use a custom address or data directory:

```bash
./dist/revu -addr 127.0.0.1:8080 -data ./data
```

Data is stored in:

- `data/revu.sqlite`
- `data/uploads/`

Back up or move a review workspace by copying the `data/` folder.

## Known Constraints

- Refresh is manual or navigation-driven; no automatic polling yet.
- There is no account login or LAN-outside access.

## Host Delete Authority

The device that runs the server is the host. Requests from loopback (`127.0.0.1` / `::1`) are treated as host requests, and the host can delete any thread or comment. Editing stays owner-only; the host has no edit rights.

Note: if the host machine opens the app via its own LAN IP (for example `http://192.168.x.x:8080`), it is not treated as host. Use the `Local: http://127.0.0.1:8080` URL shown in the startup log.

## Edit History

Editing a thread saves the previous title and body. Threads that have been edited show a 履歴 button in the thread header; anyone can open it to browse versions and a unified diff of each edit. Oversized diffs fall back to full-text display.

Note: history starts with the first edit made after this feature was introduced. Earlier edits were not recorded and cannot be recovered.

The thread header shows the current version (for example `v3`), and each comment shows the version it was posted against. On threads that have been edited, these badges are links that open the edit history at that version in a new tab. Comments posted before this feature was introduced have no recorded version and show no badge.

## Offline UI Check

The browser runtime libraries are embedded in the revu binary. After building, start the server and confirm the app does not request CDN or Google Fonts assets.

mac/Linux:

```bash
go build -o dist/revu ./cmd/revu
./dist/revu -addr 127.0.0.1:8080 -data /tmp/revu-phase2-offline
```

Windows PowerShell:

```powershell
go build -o dist/revu.exe ./cmd/revu
.\dist\revu.exe -addr 127.0.0.1:8080 -data .\data-phase2-offline
```

Open `http://127.0.0.1:8080`, then use the browser Network panel to confirm requests stay on `127.0.0.1:8080`.

## Development

Run tests and build:

```bash
go test ./... -count=1
go build -o dist/revu ./cmd/revu
```
