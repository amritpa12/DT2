import { defineEval } from "@cursor/bdk/evals";

export default defineEval({
  tags: ["smoke"],
  test: async (t) => {
    await t.send(
      "Research one trending Roblox topic and save a game idea for project obby-trend. Do not design, write requirements, build, or validate.",
    );
    t.succeeded();
    t.calledSubagent("researcher");
    t.calledTool("save_studio_note", {
      input: { project: "obby-trend", kind: "idea", authorRole: "researcher" },
    });
    t.notCalledTool("publish_place");
  },
});
