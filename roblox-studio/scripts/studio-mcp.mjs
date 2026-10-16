import { spawn } from "node:child_process";
import { existsSync } from "node:fs";

function resolveStudioMcp() {
  const override = process.env.ROBLOX_STUDIO_MCP_COMMAND;
  if (override && existsSync(override)) {
    return { command: override, args: [] };
  }
  const home = process.env.HOME ?? "";
  const macCandidates = [
    "/Applications/RobloxStudio.app/Contents/MacOS/StudioMCP",
    home ? `${home}/Applications/RobloxStudio.app/Contents/MacOS/StudioMCP` : "",
  ].filter(Boolean);
  for (const command of macCandidates) {
    if (existsSync(command)) return { command, args: [] };
  }
  if (process.platform === "win32" && process.env.LOCALAPPDATA) {
    const bat = `${process.env.LOCALAPPDATA}\\Roblox\\mcp.bat`;
    if (existsSync(bat)) return { command: "cmd.exe", args: ["/c", bat] };
  }
  return undefined;
}

const resolved = resolveStudioMcp();
if (!resolved) process.exit(1);

const child = spawn(resolved.command, resolved.args, { stdio: "inherit" });
child.on("exit", (code, signal) => {
  if (signal) process.kill(process.pid, signal);
  process.exit(code ?? 1);
});
