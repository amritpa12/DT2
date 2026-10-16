type Json =
  | string
  | number
  | boolean
  | null
  | Json[]
  | { [key: string]: Json };

export const NOTE_KINDS = [
  "idea",
  "design",
  "requirements",
  "build",
  "validation",
] as const;

export type NoteKind = (typeof NOTE_KINDS)[number];

export const STUDIO_ROLES = [
  "researcher",
  "designer",
  "product-owner",
  "developer",
  "validator",
] as const;

export type StudioRole = (typeof STUDIO_ROLES)[number];

export const ROLE_KIND: Record<StudioRole, NoteKind> = {
  researcher: "idea",
  designer: "design",
  "product-owner": "requirements",
  developer: "build",
  validator: "validation",
};

const REQUIRES: Partial<Record<NoteKind, NoteKind>> = {
  design: "idea",
  requirements: "design",
  build: "requirements",
  validation: "requirements",
};

export type RequirementResult = {
  id: string;
  result: "pass" | "fail";
};

export type StudioNote = {
  project: string;
  kind: NoteKind;
  title: string;
  body: string;
  authorRole: StudioRole;
  revision: number;
  updatedAt: string;
  requirementIds?: string[];
  results?: RequirementResult[];
  verdict?: "pass" | "fail" | "blocked";
  evidenceMode?: "playtest" | "source-review";
  studioId?: string;
  placeId?: string;
};

export type ReleaseCheck = {
  ready: boolean;
  blockers: string[];
};

const PROJECT_PATTERN = /^[a-z0-9][a-z0-9-]{0,40}$/;
const REQUIREMENT_PATTERN = /^REQ-[1-9][0-9]*$/;

export function assertProject(project: string): string {
  const slug = project.trim().toLowerCase();
  if (!PROJECT_PATTERN.test(slug)) {
    throw new Error(
      "Project slug must be 1-41 characters of lowercase letters, numbers, and hyphens.",
    );
  }
  return slug;
}

export function noteKey(project: string, kind: NoteKind): string {
  return `studio:${project}:${kind}`;
}

export function noteToJson(note: StudioNote): { [key: string]: Json } {
  const out: { [key: string]: Json } = {
    project: note.project,
    kind: note.kind,
    title: note.title,
    body: note.body,
    authorRole: note.authorRole,
    revision: note.revision,
    updatedAt: note.updatedAt,
  };
  if (note.requirementIds) out.requirementIds = note.requirementIds;
  if (note.results) {
    out.results = note.results.map((result) => ({
      id: result.id,
      result: result.result,
    }));
  }
  if (note.verdict) out.verdict = note.verdict;
  if (note.evidenceMode) out.evidenceMode = note.evidenceMode;
  if (note.studioId) out.studioId = note.studioId;
  if (note.placeId) out.placeId = note.placeId;
  return out;
}

function isRecord(value: unknown): value is { [key: string]: unknown } {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function readString(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

export function parseNote(value: unknown): StudioNote | undefined {
  if (!isRecord(value)) return undefined;
  const kind = readString(value.kind);
  const authorRole = readString(value.authorRole);
  const project = readString(value.project);
  const title = readString(value.title);
  const body = readString(value.body);
  const updatedAt = readString(value.updatedAt);
  if (
    !kind ||
    !NOTE_KINDS.includes(kind as NoteKind) ||
    !authorRole ||
    !STUDIO_ROLES.includes(authorRole as StudioRole) ||
    !project ||
    !title ||
    !body ||
    !updatedAt ||
    typeof value.revision !== "number"
  ) {
    return undefined;
  }
  const note: StudioNote = {
    project,
    kind: kind as NoteKind,
    title,
    body,
    authorRole: authorRole as StudioRole,
    revision: value.revision,
    updatedAt,
  };
  if (Array.isArray(value.requirementIds)) {
    note.requirementIds = value.requirementIds.filter(
      (id): id is string => typeof id === "string",
    );
  }
  if (Array.isArray(value.results)) {
    note.results = value.results.flatMap((entry) => {
      if (!isRecord(entry)) return [];
      const id = readString(entry.id);
      const result = readString(entry.result);
      if (!id || (result !== "pass" && result !== "fail")) return [];
      return [{ id, result }];
    });
  }
  const verdict = readString(value.verdict);
  if (verdict === "pass" || verdict === "fail" || verdict === "blocked") {
    note.verdict = verdict;
  }
  const evidenceMode = readString(value.evidenceMode);
  if (evidenceMode === "playtest" || evidenceMode === "source-review") {
    note.evidenceMode = evidenceMode;
  }
  const studioId = readString(value.studioId);
  if (studioId) note.studioId = studioId;
  const placeId = readString(value.placeId);
  if (placeId) note.placeId = placeId;
  return note;
}

export function normalizeRequirementIds(ids: readonly string[]): string[] {
  const unique = new Set<string>();
  for (const id of ids) {
    const trimmed = id.trim();
    if (!REQUIREMENT_PATTERN.test(trimmed)) {
      throw new Error(`Requirement ids look like REQ-1. Rejected: ${trimmed}`);
    }
    unique.add(trimmed);
  }
  if (unique.size === 0) {
    throw new Error("Requirements need at least one REQ id.");
  }
  return [...unique].sort((a, b) => a.localeCompare(b, "en"));
}

export function releaseCheck(
  notes: Partial<Record<NoteKind, StudioNote>>,
): ReleaseCheck {
  const blockers: string[] = [];
  const idea = notes.idea;
  const design = notes.design;
  const requirements = notes.requirements;
  const build = notes.build;
  const validation = notes.validation;
  if (!idea) blockers.push("missing idea");
  if (!design) blockers.push("missing design");
  if (!requirements) blockers.push("missing requirements");
  if (!build) blockers.push("missing build");
  if (!validation) blockers.push("missing validation");
  if (idea && design && design.updatedAt < idea.updatedAt) {
    blockers.push("design is older than the idea");
  }
  if (design && requirements && requirements.updatedAt < design.updatedAt) {
    blockers.push("requirements are older than the design");
  }
  if (requirements && build && build.updatedAt < requirements.updatedAt) {
    blockers.push("build is older than the requirements");
  }
  if (build && validation && validation.updatedAt < build.updatedAt) {
    blockers.push("validation is older than the build");
  }
  if (validation && validation.verdict !== "pass") {
    blockers.push(`validation verdict is ${validation.verdict ?? "unset"}`);
  }
  if (validation && validation.evidenceMode !== "playtest") {
    blockers.push("validation did not playtest in Studio");
  }
  if (!build?.studioId || !validation?.studioId) {
    blockers.push("build and validation need the same Studio instance id");
  } else if (build.studioId !== validation.studioId) {
    blockers.push("validation playtested a different Studio instance than the build");
  }
  if (!build?.placeId) {
    blockers.push("build has no Roblox place id");
  }
  const ids = requirements?.requirementIds ?? [];
  if (requirements && ids.length === 0) {
    blockers.push("requirements have no REQ ids");
  }
  const results = new Map((validation?.results ?? []).map((result) => [result.id, result.result]));
  for (const id of ids) {
    if (results.get(id) !== "pass") blockers.push(`${id} is not a playtest pass`);
  }
  return { ready: blockers.length === 0, blockers };
}

export function priorKind(kind: NoteKind): NoteKind | undefined {
  return REQUIRES[kind];
}
