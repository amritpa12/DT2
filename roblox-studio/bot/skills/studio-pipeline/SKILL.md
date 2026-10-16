---
description: Use when building, playtesting, or releasing a Roblox place through the open Studio session.
---

# Studio pipeline

Load this before touching Roblox Studio or `publish_place`.

## Who may call Studio

| Role | May call | Must not call |
| --- | --- | --- |
| developer | `list_roblox_studios`, `search_game_tree`, `inspect_instance`, `script_read`, `script_search`, `script_grep`, `multi_edit`, `execute_luau` with `datamodel_type` Edit, `insert_asset`, `search_asset` | playtest controls, `publish_place` |
| validator | `list_roblox_studios`, `get_studio_state`, `start_stop_play`, `execute_luau` with Client or Server, `get_console_output`, `screen_capture`, `character_navigation`, `user_keyboard_input`, `user_mouse_input`, `script_read`, `script_grep` | `multi_edit`, Edit-mode `execute_luau`, `insert_asset`, `publish_place` |
| everyone else | none | every Studio tool |

Pass `studio_id` from `list_roblox_studios` on every Studio call. If two places are open, use the place id to pick the right one. Stop if the tools are absent and say Studio is not connected.

## Build

Create scripts with `multi_edit` at real paths such as `game.ServerScriptService.GameServer`. Keep server authority on the server. Do not trust the client for rewards, wins, or currency.

Record a build note with:

- the `studio_id` you used
- the place id when Studio lists one
- the script paths you created or changed
- how a player starts and finishes a session

A local place with no place id cannot be released. The user publishes it once from Studio, then you record that place id.

## Playtest

Start play with `start_stop_play`. Exercise each `REQ-*` from the requirements. Read `get_console_output` after each check. Stop play when finished. Write what you observed, including the console line or the instance path that proved it.

A pass requires `evidenceMode: "playtest"`, the same `studioId` as the build, and every requirement id marked `pass`. If you could not playtest, save `evidenceMode: "source-review"` and `verdict: "blocked"`.

## Release

`publish_place` refuses unless the board is ready, the place file is a `.rbxl` or `.rbxlx` in the session workspace or `ROBLOX_PLACES_DIR`, `ROBLOX_OPEN_CLOUD_API_KEY` is set with `universe-places:write`, and the playtested Studio instance is still connected.

The API key never goes in a note, a chat reply, or a tool argument. Ask the user to set it in the environment.
