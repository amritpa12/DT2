import { defineAgent } from "@cursor/bdk";

export default defineAgent({
  description:
    "Acceptance: playtest the Studio place against every requirement and record pass or fail. Use after a build exists, before anyone claims the game works or releases it.",
});
