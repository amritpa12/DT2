import { readFile } from "node:fs/promises";
import { defineTool } from "@cursor/bdk/tools";
import { z } from "zod/v3";
import {
  NOTE_KINDS,
  type NoteKind,
  type StudioNote,
  assertProject,
  noteKey,
  parseNote,
  releaseCheck,
} from "../lib/board.js";
import { resolvePlaceFile } from "../lib/place-file.js";

const PublishInput = z.object({
  project: z.string(),
  universeId: z.string().regex(/^[1-9][0-9]{0,18}$/),
  placeId: z.string().regex(/^[1-9][0-9]{0,18}$/),
  placeFilePath: z.string().min(1),
  versionType: z.enum(["Saved", "Published"]),
});

type PublishResult = {
  published: boolean;
  versionType: "Saved" | "Published";
  universeId: string;
  placeId: string;
  versionNumber: string;
};

function redact(text: string, secret: string): string {
  if (!secret) return text;
  return text.split(secret).join("[redacted]");
}

export default defineTool({
  description:
    "Upload a saved .rbxl or .rbxlx to an existing Roblox experience after a passing Studio playtest. versionType Published releases it to players. versionType Saved stores a version without going live. Needs human approval. Does not create a new experience.",
  effect: "write",
  needsApproval: true,
  inputSchema: PublishInput,
  dryRunResult: ({ universeId, placeId, versionType }) => ({
    published: false,
    dryRun: true,
    universeId,
    placeId,
    versionType,
  }),
  async execute(input, ctx): Promise<PublishResult> {
    if (ctx.session.purpose === "eval") {
      throw new Error("Refusing to publish during an eval.");
    }
    const project = assertProject(input.project);
    const notes: Partial<Record<NoteKind, StudioNote>> = {};
    for (const kind of NOTE_KINDS) {
      const note = parseNote(
        await ctx.host.kv.get(noteKey(project, kind)),
      );
      if (note) notes[kind] = note;
    }
    const release = releaseCheck(notes);
    if (!release.ready) {
      throw new Error(`Release is blocked: ${release.blockers.join("; ")}.`);
    }
    if (notes.build?.placeId !== input.placeId) {
      throw new Error("placeId does not match the place id recorded on the build.");
    }
    const place = await resolvePlaceFile(ctx.workspaceDir, input.placeFilePath);
    const apiKey = process.env.ROBLOX_OPEN_CLOUD_API_KEY ?? "";
    if (!apiKey) {
      throw new Error(
        "Set ROBLOX_OPEN_CLOUD_API_KEY with universe-places:write on this experience.",
      );
    }
    const studioId = notes.validation?.studioId ?? "";
    try {
      const listed = await ctx.host.mcp.callTool("studio", "list_roblox_studios", {});
      const text = listed.content
        .map((item) => ("text" in item ? item.text : ""))
        .join("\n");
      if (!text.includes(studioId)) {
        throw new Error("The playtested Studio instance is not connected.");
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : "Studio MCP failed";
      if (message === "The playtested Studio instance is not connected.") throw error;
      throw new Error(
        `Roblox Studio is not connected, so this place cannot be released. ${message}`,
      );
    }
    const url =
      `https://apis.roblox.com/universes/v1/${input.universeId}` +
      `/places/${input.placeId}/versions?versionType=${input.versionType}`;
    const response = await fetch(url, {
      method: "POST",
      redirect: "manual",
      headers: {
        "x-api-key": apiKey,
        "content-type": place.contentType,
      },
      body: await readFile(place.path),
      signal: AbortSignal.timeout(120_000),
    });
    if (response.status >= 300 && response.status < 400) {
      throw new Error(
        `Place upload was redirected (${response.status}). Refusing to follow so the API key stays on apis.roblox.com.`,
      );
    }
    const raw = await response.text();
    if (!response.ok) {
      throw new Error(
        `Roblox place upload failed (${response.status}): ${redact(raw, apiKey).slice(0, 500)}`,
      );
    }
    let versionNumber = raw.trim();
    try {
      const parsed: unknown = JSON.parse(raw);
      if (typeof parsed === "object" && parsed !== null && "versionNumber" in parsed) {
        versionNumber = String(parsed.versionNumber);
      }
    } catch {
      versionNumber = raw.trim();
    }
    return {
      published: input.versionType === "Published",
      versionType: input.versionType,
      universeId: input.universeId,
      placeId: input.placeId,
      versionNumber,
    };
  },
});
