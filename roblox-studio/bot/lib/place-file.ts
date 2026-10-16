import { realpath, stat } from "node:fs/promises";
import path from "node:path";

const MAX_PLACE_BYTES = 100 * 1024 * 1024;

export type PlaceFile = {
  path: string;
  bytes: number;
  contentType: "application/octet-stream" | "application/xml";
};

function insideRoot(filePath: string, root: string): boolean {
  const relative = path.relative(root, filePath);
  return relative === "" || (!relative.startsWith("..") && !path.isAbsolute(relative));
}

export async function resolvePlaceFile(
  workspaceDir: string,
  requested: string,
): Promise<PlaceFile> {
  const roots = [workspaceDir];
  if (process.env.ROBLOX_PLACES_DIR) {
    roots.push(process.env.ROBLOX_PLACES_DIR);
  }
  const realRoots: string[] = [];
  for (const root of roots) {
    try {
      realRoots.push(await realpath(root));
    } catch {
      continue;
    }
  }
  let realFile: string;
  try {
    realFile = await realpath(requested);
  } catch {
    throw new Error("Place file was not found. Save the place from Studio first.");
  }
  if (!realRoots.some((root) => insideRoot(realFile, root))) {
    throw new Error(
      "Place file must sit in the session workspace or ROBLOX_PLACES_DIR.",
    );
  }
  const lower = realFile.toLowerCase();
  const contentType = lower.endsWith(".rbxlx")
    ? "application/xml"
    : lower.endsWith(".rbxl")
      ? "application/octet-stream"
      : undefined;
  if (!contentType) {
    throw new Error("Place file must end in .rbxl or .rbxlx.");
  }
  const info = await stat(realFile);
  if (!info.isFile()) {
    throw new Error("Place path is not a file.");
  }
  if (info.size <= 0 || info.size > MAX_PLACE_BYTES) {
    throw new Error("Place file must be between 1 byte and 100 MB.");
  }
  return { path: realFile, bytes: info.size, contentType };
}
