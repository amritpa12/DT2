import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { defineConnection } from "@cursor/bdk/connections";

const projectRoot = resolve(dirname(fileURLToPath(import.meta.url)), "../..");

export default defineConnection({
  description:
    "Roblox Studio on this machine. Appears only while Studio is open with its MCP server enabled.",
  command: "node",
  args: [resolve(projectRoot, "scripts/studio-mcp.mjs")],
  cwd: projectRoot,
  advertiseTools: true,
  optional: true,
});
