# Roblox Studio

A game studio agent. Researchers turn a trend into an idea, designers shape the core loop, product owners write requirements, developers build in the open Roblox Studio place, and validators playtest before anything is released.

## Connect Studio

Run this on the same computer as Roblox Studio. Studio has no supported MCP server on Linux.

1. Open the place in Roblox Studio.
2. Assistant, then Manage MCP Servers, then turn on **Enable Studio as MCP server**.
3. From this directory, install dependencies, sign in, and start the agent:

```bash
npm install
npx @cursor/bdk login
npx @cursor/bdk dev
```

The agent launches Studio's MCP server: `StudioMCP` on macOS, or `%LOCALAPPDATA%\Roblox\mcp.bat` on Windows. Set `ROBLOX_STUDIO_MCP_COMMAND` when that binary lives somewhere else.

The first release of a brand-new place still happens once from Studio, which creates the universe id and place id. Later saves and releases go through `publish_place`.

## Release

Set `ROBLOX_OPEN_CLOUD_API_KEY` in the environment. The key needs `universe-places:write` on that experience. Do not put the key in chat or in the repo.

`publish_place` asks for approval, then uploads a `.rbxl` or `.rbxlx` only when the board has a passing Studio playtest for that place. `Published` goes live to players. `Saved` stores a version without releasing it. The place file must be in the session workspace or in `ROBLOX_PLACES_DIR`.
