# gmod_mcp

MCP server for controlling and inspecting a local Garry's Mod client on Windows.

It can:

- launch Garry's Mod through Steam
- detect whether the game is open, ready, in the menu, in singleplayer, or connected to a server
- run Source Engine console commands
- read the developer console history directly from memory

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

[Install gmod_mcp in Cursor](cursor://anysphere.cursor-deeplink/mcp/install?name=gmod&config=eyJjb21tYW5kIjoibnB4IiwiYXJncyI6WyIteSIsImdpdGh1YjpTaGFhci1nYW1lcy9nbW9kX21jcCNsYXRlc3QiXX0%3D)

If your browser does not open Cursor from the link, copy and paste this into the address bar:

```text
cursor://anysphere.cursor-deeplink/mcp/install?name=gmod&config=eyJjb21tYW5kIjoibnB4IiwiYXJncyI6WyIteSIsImdpdGh1YjpTaGFhci1nYW1lcy9nbW9kX21jcCNsYXRlc3QiXX0%3D
```

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

### `RunConsoleCommand`

Executes a Source Engine console command in the running Garry's Mod process.

Parameters:

| Parameter | Type | Required | Description |
| --- | --- | --- | --- |
| `command` | string | Yes | Console command to execute, for example `status` or `say hello`. |
| `processName` | string | No | Process image name to search for. Default: `gmod.exe`. |
| `pid` | int | No | Specific Garry's Mod process id. Overrides `processName`. |

### `GetConsoleOutput`

Reads the developer console history directly from `engine.dll` memory.

It walks the same linked list used by the engine crash dump path for `-Console Buffer-`, then returns the newest lines in chronological order.

Parameters:

| Parameter | Type | Required | Description |
| --- | --- | --- | --- |
| `lines` | int | No | Maximum recent lines to return. Default: `100`, max: `1000`. |
| `processName` | string | No | Process image name to search for. Default: `gmod.exe`. |
| `pid` | int | No | Specific Garry's Mod process id. Overrides `processName`. |

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

## Technical Notes

- `RunConsoleCommand` resolves `IVEngineClient015` from `client.dll` at RVA `0x7BD390`.
- `ClientCmd` is called through vtable offset `28` using an injected x86 thiscall stub.
- `GetGameStatus` checks `IVEngineClient::IsInGame` through vtable offset `0x68`.
- Local/server classification uses the clientside game rules max-player value at `client.dll` RVA `0x717050` plus offset `0x1C`.
- `GetConsoleOutput` reads `engine.dll` globals at RVAs `0x42F070` and `0x42F07C`.
- These RVAs are tied to the analyzed Garry's Mod build and may need updates after game patches.

## License

MIT
