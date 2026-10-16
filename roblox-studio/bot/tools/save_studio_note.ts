import { defineTool } from "@cursor/bdk/tools";
import { z } from "zod/v3";
import {
  ROLE_KIND,
  type NoteKind,
  type StudioNote,
  type StudioRole,
  assertProject,
  normalizeRequirementIds,
  noteKey,
  noteToJson,
  parseNote,
  priorKind,
  releaseCheck,
} from "../lib/board.js";

const Result = z.object({
  id: z.string(),
  result: z.enum(["pass", "fail"]),
});

export default defineTool({
  description:
    "Save one handoff on the studio board. researcher saves an idea, designer saves a design, product-owner saves requirements, developer saves a build, and validator saves a validation. Saving the same kind again replaces that stage.",
  effect: "write",
  dryRunResult: ({ project, kind, title }) => ({
    saved: false,
    dryRun: true,
    project,
    kind,
    title,
  }),
  inputSchema: z.object({
    project: z.string(),
    kind: z.enum(["idea", "design", "requirements", "build", "validation"]),
    authorRole: z.enum([
      "researcher",
      "designer",
      "product-owner",
      "developer",
      "validator",
    ]),
    title: z.string().min(1).max(200),
    body: z.string().min(1).max(100_000),
    requirementIds: z.array(z.string()).optional(),
    results: z.array(Result).optional(),
    verdict: z.enum(["pass", "fail", "blocked"]).optional(),
    evidenceMode: z.enum(["playtest", "source-review"]).optional(),
    studioId: z.string().min(1).max(200).optional(),
    placeId: z.string().regex(/^[1-9][0-9]{0,18}$/).optional(),
  }),
  async execute(input, ctx) {
    const project = assertProject(input.project);
    const kind = input.kind as NoteKind;
    const authorRole = input.authorRole as StudioRole;
    if (ROLE_KIND[authorRole] !== kind) {
      throw new Error(`${authorRole} records ${ROLE_KIND[authorRole]}, not ${kind}.`);
    }
    const required = priorKind(kind);
    const prior = required
      ? parseNote(await ctx.host.kv.get(noteKey(project, required)))
      : undefined;
    if (required && !prior) {
      throw new Error(`Save the ${required} before the ${kind}.`);
    }

    const note: StudioNote = {
      project,
      kind,
      title: input.title.trim(),
      body: input.body.trim(),
      authorRole,
      revision: 1,
      updatedAt: new Date().toISOString(),
    };
    if (input.studioId) note.studioId = input.studioId.trim();
    if (input.placeId) note.placeId = input.placeId;

    if (kind === "requirements") {
      const ids = normalizeRequirementIds(input.requirementIds ?? []);
      for (const id of ids) {
        if (!note.body.includes(id)) {
          throw new Error(`Requirements body must mention ${id}.`);
        }
      }
      note.requirementIds = ids;
    }

    if (kind === "validation") {
      if (!input.verdict || !input.evidenceMode || !input.results) {
        throw new Error("Validation needs a verdict, evidenceMode, and results.");
      }
      const requirements = parseNote(
        await ctx.host.kv.get(noteKey(project, "requirements")),
      );
      const ids = requirements?.requirementIds ?? [];
      if (ids.length === 0) {
        throw new Error("Validation needs requirements that list REQ ids.");
      }
      const results = new Map(input.results.map((result) => [result.id, result.result]));
      for (const id of ids) {
        if (!results.has(id)) throw new Error(`Validation is missing ${id}.`);
      }
      for (const id of results.keys()) {
        if (!ids.includes(id)) throw new Error(`${id} is not in the requirements.`);
      }
      note.verdict = input.verdict;
      note.evidenceMode = input.evidenceMode;
      note.results = ids.map((id) => ({ id, result: results.get(id)! }));
      if (input.verdict === "pass") {
        const build = parseNote(
          await ctx.host.kv.get(noteKey(project, "build")),
        );
        const idea = parseNote(
          await ctx.host.kv.get(noteKey(project, "idea")),
        );
        const design = parseNote(
          await ctx.host.kv.get(noteKey(project, "design")),
        );
        const check = releaseCheck({
          idea,
          design,
          requirements,
          build,
          validation: note,
        });
        if (!check.ready) {
          throw new Error(`Cannot record a pass: ${check.blockers.join("; ")}.`);
        }
      }
    }

    const existing = parseNote(
      await ctx.host.kv.get(noteKey(project, kind)),
    );
    note.revision = (existing?.revision ?? 0) + 1;
    const stored = noteToJson(note);
    await ctx.host.kv.put(noteKey(project, kind), stored);
    await ctx.artifacts.tag({
      kind,
      key: noteKey(project, kind),
      title: note.title,
      data: stored,
      contents: note.body,
      contentType: "text/markdown",
    });
    return {
      saved: true,
      project,
      kind,
      revision: note.revision,
      updatedAt: note.updatedAt,
    };
  },
});
