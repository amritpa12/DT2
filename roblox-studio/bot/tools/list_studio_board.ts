import { defineTool } from "@cursor/bdk/tools";
import { z } from "zod/v3";
import {
  NOTE_KINDS,
  type NoteKind,
  type StudioNote,
  assertProject,
  noteKey,
  noteToJson,
  parseNote,
  releaseCheck,
} from "../lib/board.js";

export default defineTool({
  description:
    "Read the idea, design, requirements, build, and validation saved for one project, and whether that project is allowed to release.",
  effect: "read",
  inputSchema: z.object({
    project: z.string(),
  }),
  async execute({ project: rawProject }, ctx) {
    const project = assertProject(rawProject);
    const notes: Partial<Record<NoteKind, StudioNote>> = {};
    for (const kind of NOTE_KINDS) {
      const note = parseNote(
        await ctx.host.kv.get(noteKey(project, kind)),
      );
      if (note) notes[kind] = note;
    }
    const release = releaseCheck(notes);
    const ordered: Record<string, ReturnType<typeof noteToJson> | null> = {};
    for (const kind of NOTE_KINDS) {
      const note = notes[kind];
      ordered[kind] = note ? noteToJson(note) : null;
    }
    return { project, notes: ordered, release };
  },
});
