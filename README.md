# gmod_mcp

MCP server for controlling and inspecting a local Garry's Mod client.

The server is Windows-only and talks to the running `gmod.exe` process directly. It can launch the game through Steam, detect whether the main game client is ready, run Source Engine console commands, and read the developer console history from process memory.

## Requirements

- Windows
- Go 1.24+ for building from source
- Garry's Mod installed through Steam
- A 32-bit build target (`GOARCH=386`)
- The MCP server must run as a user that can open the Garry's Mod process

## Build

PowerShell:

```powershell
.\build.cmd
```

The script builds `bin\gmod_mcp.exe` with `GOOS=windows` and `GOARCH=386`.

Clean rebuild:

```powershell
.\build.cmd clean
```

`GOARCH=386` is required because Garry's Mod is a 32-bit process.

## MCP Configuration

Cursor project config example:

```json
{
  "mcpServers": {
    "gmod": {
      "command": "C:\\Users\\shaar\\Documents\\Dev\\gmod_mcp\\bin\\gmod_mcp.exe"
    }
  }
}
```

After rebuilding `bin\gmod_mcp.exe`, reload Cursor or restart the MCP server so the new binary is used.

## Tools

### `LaunchGame`

Asks Steam to launch Garry's Mod with `steam://run/4000`, then waits until the main game process is ready or a timeout is reached.

This tool does not expose an app id. It is intentionally locked to Garry's Mod.

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

## Architecture

```text
gmod_mcp/
|-- main.go
|-- go.mod
|-- cmd/
|   |-- gamestatus/
|   |-- launchgame/
|   |-- readconsole/
|   `-- runconsole/
|-- internal/
|   `-- gmod/
|       |-- gmod.go
|       `-- proc_windows.go
`-- README.md
```

## Technical Notes

- `RunConsoleCommand` resolves `IVEngineClient015` from `client.dll` at RVA `0x7BD390`.
- `ClientCmd` is called through vtable offset `28` using an injected x86 thiscall stub.
- `GetGameStatus` checks `IVEngineClient::IsInGame` through vtable offset `0x68`.
- Local/server classification uses the clientside game rules max-player value at `client.dll` RVA `0x717050` plus offset `0x1C`.
- `GetConsoleOutput` reads `engine.dll` globals at RVAs `0x42F070` and `0x42F07C`.
- These RVAs are tied to the analyzed Garry's Mod build and may need updates after game patches.

## Distribution Notes

For GitHub-based distribution without committing `gmod_mcp.exe`, publish the binary as a GitHub Release asset and use an installer or launcher script to download it locally. The MCP client still needs to execute a local binary because the server must access the local Garry's Mod process.

## License

MIT
