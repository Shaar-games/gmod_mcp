# gmod_mcp

MCP server for controlling and inspecting a local Garry's Mod client on Windows.

It can:

- launch Garry's Mod through Steam
- detect whether the game is open, ready, in the menu, in singleplayer, or connected to a server
- run Source Engine console commands
- read the developer console history directly from memory
- notify MCP clients when the game becomes ready, stops responding, recovers, or exits, with available crash diagnostics

The MCP server must run on the same Windows machine as Garry's Mod.

## Requirements

- Windows
- Garry's Mod installed through Steam
- Node.js/npm if installing through `npx`
- The release asset `gmod_mcp.exe` must exist on the GitHub release/tag used by the installer

## Quick Install

This project is designed to run through GitHub with `npx`. The repository must be public for the `github:` npm shortcut to work.

```powershell
npx -y github:Shaar-games/gmod_mcp#latest
```

During install, npm downloads `gmod_mcp.exe` from:

```text
https://github.com/Shaar-games/gmod_mcp/releases/download/latest/gmod_mcp.exe
```

To use a different release tag, install that git tag and set the release tag used by the downloader:

```powershell
$env:GMOD_MCP_RELEASE_TAG = "v1.0.0"
npx -y github:Shaar-games/gmod_mcp#v1.0.0
```

## Cursor

### One-Click Install

[![Install MCP Server](https://cursor.com/deeplink/mcp-install-dark.svg)](https://cursor.com/en-US/install-mcp?name=gmod&config=eyJjb21tYW5kIjoibnB4IC15IGdpdGh1YjpTaGFhci1nYW1lcy9nbW9kX21jcCNsYXRlc3QifQ%3D%3D)

Cursor will show the command before installing it. It should be:

```json
{
  "command": "npx",
  "args": ["-y", "github:Shaar-games/gmod_mcp#latest"]
}
```

### Manual Cursor Config

Add this MCP server in Cursor settings, or place it in your Cursor MCP config:

```json
{
  "mcpServers": {
    "gmod": {
      "command": "npx",
      "args": ["-y", "github:Shaar-games/gmod_mcp#latest"]
    }
  }
}
```

Restart/reload Cursor after changing MCP configuration.

## Claude

### Claude Desktop

Open Claude Desktop settings, go to Developer settings, then edit the MCP configuration. Add:

```json
{
  "mcpServers": {
    "gmod": {
      "command": "npx",
      "args": ["-y", "github:Shaar-games/gmod_mcp#latest"]
    }
  }
}
```

Restart Claude Desktop after saving the config.

### Claude Code

Claude Code can add the same stdio server from JSON:

```powershell
claude mcp add-json gmod "{\"type\":\"stdio\",\"command\":\"npx\",\"args\":[\"-y\",\"github:Shaar-games/gmod_mcp#latest\"]}" --scope user
```

Verify:

```powershell
claude mcp get gmod
```

## Codex

Add the server to `~/.codex/config.toml`:

```toml
[mcp_servers.gmod]
command = "npx"
args = ["-y", "github:Shaar-games/gmod_mcp#latest"]
enabled = true
```

Restart Codex after editing the config.

## GitHub Copilot / VS Code

For GitHub Copilot Chat in VS Code, create or edit `.vscode/mcp.json` in your workspace:

```json
{
  "servers": {
    "gmod": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "github:Shaar-games/gmod_mcp#latest"]
    }
  }
}
```

Then use the VS Code command palette:

```text
MCP: List Servers
```

Start the `gmod` server if VS Code does not start it automatically.

## Release Tag and Asset Name

By default, the npm installer downloads from the `latest` release tag and looks for `gmod_mcp.exe`.

Use another release tag:

```powershell
$env:GMOD_MCP_RELEASE_TAG = "v1.0.0"
npx -y github:Shaar-games/gmod_mcp#v1.0.0
```

Use a custom release asset name:

```powershell
$env:GMOD_MCP_ASSET_NAME = "my-custom-name.exe"
npx -y github:Shaar-games/gmod_mcp#latest
```

## Tools

### `GetGameEvents`

Reads the newest 32 observed Garry's Mod readiness, responsiveness and stop/crash events, in chronological order. Events are kept in memory until the MCP server restarts, even if the client does not enable notifications.

| Parameter | Type | Required | Description |
| --- | --- | --- | --- |
| `afterId` | integer | No | Return only events newer than this ID. Default: all retained events. |

The response contains `events` and `lastEventId`. Pass `lastEventId` as `afterId` on the next call to retrieve new events. IDs reset when the MCP server restarts. If more than 32 events occur between reads, older events are no longer available.

See [Game lifecycle notifications](#game-lifecycle-notifications) for delivery options and diagnostic fields.

### `LaunchGame`

Launches Garry's Mod through Steam with `steam://run/4000`, then waits until the main game process is ready or a timeout is reached.

This tool is locked to Garry's Mod. It does not expose a configurable Steam app id.

Parameters:

| Parameter | Type | Required | Description |
| --- | --- | --- | --- |
| `timeoutSeconds` | int | No | Maximum seconds to wait for readiness. Default: `90`, max: `600`. |

Readiness means:

- a useful `gmod.exe` process exists
- `client.dll` and `engine.dll` are loaded
- `IVEngineClient015` is available (`engineApi: true`)

### `GetGameStatus`

Checks whether Garry's Mod is running and whether the main game process is ready.

It also calls `IVEngineClient::IsInGame` and classifies the session:

| `sessionType` | Meaning |
| --- | --- |
| `not_running` | No `gmod.exe` process was found. |
| `loading` | A relevant process exists, but required modules or engine interface are not ready yet. |
| `menu` | Game is open, but not currently in a map. |
| `local` | In-game with `maxPlayers == 1`. |
| `server` | In-game with `maxPlayers > 1`. |
| `unknown` | In-game, but max player count could not classify local/server. |

Parameters:

| Parameter | Type | Required | Description |
| --- | --- | --- | --- |
| `processName` | string | No | Process image name to search for. Default: `gmod.exe`. |

Process enumeration failures return `success: false` and MCP `isError: true`; `not_running` is reserved for a completed search with no match.

### `RunConsoleCommand`

Executes a Source Engine console command and waits for engine-side confirmation, returning the captured console output and recognized errors. A successful `ClientCmd` call alone is not reported as execution success.

Parameters:

| Parameter | Type | Required | Description |
| --- | --- | --- | --- |
| `command` | string | Yes | Console command to execute, for example `status` or `say hello`. |
| `processName` | string | No | Process image name to search for. Default: `gmod.exe`. |
| `pid` | int | No | Specific Garry's Mod process id. Overrides `processName`. |
| `timeoutSeconds` | int | No | Maximum execution confirmation/capture wait. Default: `5`, max: `30`. Native submission also uses bounded waits. |

The response preserves `success`, `message`, `pid` and `command`, and adds:

| Field | Meaning |
| --- | --- |
| `status` | `executed`, `error`, or `unconfirmed`. |
| `executionConfirmed` | The engine printed the unique end marker after processing this command buffer. |
| `output` | Console text captured for this invocation, with internal history prefixes and markers removed. |
| `errors` | Recognized engine/Lua error messages, such as `Unknown command`, `[ERROR]` or cheat restrictions. |
| `timedOut` | The confirmation/capture deadline expired. |
| `outputTruncated` | History eviction or the 64 KiB output limit omitted text. |
| `captureError` | The reason execution or complete capture could not be confirmed, when applicable. |

Commands are bracketed with unique `echo` markers and serialized per PID within the server, so repeated output does not get mistaken for an earlier command. Capture continues for 500 ms after the end marker to include short asynchronous replies. It reads up to 1000 recent console entries and returns at most 64 KiB of output and 64 KiB of recognized error text. `success: true` requires confirmed processing with no recognized console error; failures and unconfirmed results also set MCP `isError: true`.

`executed` confirms processing, not every possible semantic outcome: Source commands have no general success/error return code, and addons can emit arbitrary diagnostics. Errors are identified from their console text. Unrelated game output in the same time window may appear in the response, and asynchronous failures after capture ends cannot be included. Commands that exit the game, clear history or change output filtering can prevent complete confirmation; these return `unconfirmed` rather than assumed success.

### `GetCrashReport`

Extracts a bounded summary from a local Windows `.mdmp` or `.dmp` file. Pass its absolute `path`. The response includes the exception code and name, faulting thread and address, faulting module and module-relative offset, and access type/address for access violations. It also lists up to 16 addresses found in the faulting thread's stack that fall inside loaded modules. Those addresses are heuristic candidates, not a symbolized or verified call stack. Malformed or incomplete dumps return an error.

### `RunExperiment`

Runs one console probe and saves a structured evidence record containing command confirmation/output, game executable and loaded module SHA-256 hashes, optional test asset hashes, and any observed `game_stopped` or `game_crashed` event for that PID. It uses the same command confirmation as `RunConsoleCommand`.

| Parameter | Type | Required | Description |
| --- | --- | --- | --- |
| `command` | string | Yes | Console command or Lua probe to execute. |
| `observeSeconds` | int | No | Seconds to watch for an exit after command capture. Default: `5`, max: `60`. |
| `filePaths` | string[] | No | Up to eight local test assets to hash before the command. |
| `processName` | string | No | Process image name. Default: `gmod.exe`. |
| `pid` | int | No | Specific process id. Overrides `processName`. |

The tool returns the evidence at `evidencePath` under the user's cache directory. `success` means the record was saved; inspect `commandResult.status` and `outcome` separately. A command that crashes the game may be `unconfirmed` while `outcome` is `game_crashed`. `no_exit_observed` means no exit event arrived during the observation window, not that the game will remain running. Hash failures are recorded per file. Each file is limited to 512 MiB; file contents are not included in the response.

### `GetConsoleOutput`

Reads the developer console history directly from `engine.dll` memory.

It walks the same linked list used by the engine crash dump path for `-Console Buffer-`, then returns the newest lines in chronological order.

Parameters:

| Parameter | Type | Required | Description |
| --- | --- | --- | --- |
| `lines` | int | No | Maximum recent lines to return. Default: `100`, max: `1000`. |
| `processName` | string | No | Process image name to search for. Default: `gmod.exe`. |
| `pid` | int | No | Specific Garry's Mod process id. Overrides `processName`. |

## Game lifecycle notifications

The server automatically watches the default `gmod.exe` client, including a game already running when the MCP server starts. It polls once per second, attaches once `engine.dll` is loaded, and keeps a Windows process handle to detect exits and retrieve the actual exit code. Console snapshots only read memory. Readiness uses the same `CreateInterface` probe as `GetGameStatus`, with a bounded native call; it does not execute console commands.

Five event types are available:

- `game_ready`: emitted once per observed PID when `client.dll` and `engine.dll` are loaded, the engine interface's `ClientCmd` entry is accessible, and the main game window responds. A game already ready when the server attaches also produces this event.
- `game_unresponsive`: the main game window has failed bounded `WM_NULL` probes continuously for at least ten seconds. Short pauses do not emit an event. This detects message-loop responsiveness; long loading operations can also trigger it.
- `game_responsive`: emitted once when a previously reported unresponsive window answers again. Both responsiveness events include `unresponsiveSince` and `unresponsiveMilliseconds`.
- `game_stopped`: exit code `0` without a new or changed crash report.
- `game_crashed`: a nonzero exit code or a new/changed crash report. Forced termination can also produce this event; it is evidence of an abnormal exit, not proof of an engine exception.

Each event includes `id`, `type`, `pid` and `occurredAt` (UTC detection time). `exitCode` is meaningful only for stop/crash events. Missing windows and probe access errors are treated as unknown; they do not imply a freeze or recovery. Exiting during a freeze produces a stop/crash event, without a recovery event. The window probe uses [Windows `SendMessageTimeout`](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-sendmessagetimeoutw) with a 100 ms timeout.

The watcher waits three seconds after detecting an exit to allow crash files to finish writing. A process that exits before the watcher attaches, or a crash report written after that window, may be missed. Native engine calls have a two-second execution wait; a timed-out call retains its buffers until its thread finishes, and further native calls to that PID wait rather than creating more stuck threads.

### Receiving events

The server supports the standard MCP [logging notifications](https://modelcontextprotocol.io/specification/2025-11-25/server/utilities/logging):

1. Have the client send `logging/setLevel` with `{"level":"notice"}` (or a lower level such as `info`). This SDK sends no log notifications until the client sets a level.
2. Listen for `notifications/message` with `logger: "gmod.lifecycle"`. Ready, recovery and stop events use level `notice`, unresponsiveness uses `warning`, and crashes use `error`; the structured event is in `params.data`.

Alternatively, subscribe to the `gmod://events` resource using `resources/subscribe`. The server sends `notifications/resources/updated` for that URI when an event is recorded; read it with `resources/read` to retrieve the history. Unsubscribe using `resources/unsubscribe`.

Clients that do not support either notification mechanism can call `GetGameEvents`. Notification display and whether an assistant reacts automatically are controlled by the MCP client; the server cannot force a new assistant turn.

### Crash diagnostics

For abnormal exits, the event includes the most recent successful snapshot of up to 100 console lines (maximum 64 KiB), with `consoleCapturedAt`. The snapshot can precede the fatal error; `consoleError` describes the latest failed read, when applicable. A crash during startup may have no console snapshot.

The watcher also scans the executable's game directory and its `crashes/` subdirectory for new or changed `.mdmp`/`.dmp` files and crash text reports. These are the [crash dump locations documented by Facepunch](https://wiki.facepunch.com/gmod/Crash_Reporting). Executables under `bin/win32` or `bin/win64` are resolved back to the game directory.

`crashFiles` includes absolute paths and sizes for up to eight recent reports. Binary dumps include the same bounded summary as `GetCrashReport` when parsing succeeds; their raw contents are not embedded. Text `.log`/`.txt` reports in `crashes/`, and text files containing `crash` in their name in the game root, include their tail within a shared 64 KiB budget; `truncated` indicates omitted bytes. Existing unchanged files are excluded using a baseline taken when the watcher attaches. File access and dump parsing errors, and report limits, appear in `reportErrors`. Reports are associated by directory and changes during monitoring; with multiple game instances sharing a directory, a report cannot be attributed conclusively to one PID.

## Troubleshooting

### `package.json` not found

Make sure you are using a tag that includes the npm wrapper files:

```powershell
npx -y github:Shaar-games/gmod_mcp#latest
```

If npm cached an old broken install:

```powershell
npm cache clean --force
```

### Release asset download fails

Check that this URL exists in a browser:

```text
https://github.com/Shaar-games/gmod_mcp/releases/download/latest/gmod_mcp.exe
```

If you use another tag, replace `latest` with that tag.

### MCP starts but no tools appear

Restart the MCP client after installation. Some clients cache the available tools until the server is restarted.

## Building From Source

This section is for contributors.

Requirements:

- Windows
- Go 1.24+

Build:

```powershell
.\build.cmd
```

The script builds `bin\gmod_mcp.exe` with `GOOS=windows` and `GOARCH=386`.

Clean rebuild:

```powershell
.\build.cmd clean
```

`GOARCH=386` is required because Garry's Mod is a 32-bit process.

## Creating a GitHub Release

This section is for maintainers.

Requirements:

- GitHub CLI installed
- authenticated with `gh auth login`
- tracked git files committed before release

Create a release from the current branch:

```powershell
.\release.cmd v1.3.0
```

Overwrite an existing release/tag intentionally:

```powershell
.\release.cmd v1.3.0 --force
```

The script:

- builds `bin\gmod_mcp.exe`
- tags the current commit with the tag argument
- pushes the tag to `origin`
- creates a GitHub release
- uploads `bin\gmod_mcp.exe` as `gmod_mcp.exe`
- uses this release note:

```text
Full Changelog: https://github.com/Shaar-games/gmod_mcp/commits/{tag name}
```

Without `--force`, the script refuses to overwrite an existing local tag, remote tag, or GitHub release.

## Local CLI Tests

The `cmd/` programs are small debug entry points for testing behavior without MCP.

```powershell
$env:GOOS = "windows"
$env:GOARCH = "386"
```

Check status:

```powershell
go run ./cmd/gamestatus
```

Launch through Steam and wait for readiness:

```powershell
go run ./cmd/launchgame -timeout 90
```

Run a console command:

```powershell
go run ./cmd/runconsole status
go run ./cmd/runconsole "say hello from cli"
```

Read console output from memory:

```powershell
go run ./cmd/readconsole -lines 50
```

Run the automated tests (including Windows process exit detection and MCP notification/resource delivery):

```powershell
$env:GOOS = "windows"
$env:GOARCH = "386"
go test ./...
```

## Technical Notes

- `IVEngineClient015` is resolved dynamically by calling `engine.dll!CreateInterface` inside the target process.
- `RunConsoleCommand` calls `IVEngineClient::ClientCmd` through the `IVEngineClient015` vtable.
- `GetGameStatus` calls `IVEngineClient::IsInGame` and `IVEngineClient::GetMaxClients` through the same interface.
- `GetConsoleOutput` signature-scans `engine.dll` for the console dump walker and extracts the runtime console-list globals from the matched instructions.
- The remaining vtable offsets are tied to the `IVEngineClient015` ABI instead of module RVAs.

## License

MIT
