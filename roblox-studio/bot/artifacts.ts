import { defineArtifacts } from "@cursor/bdk/artifacts";

export default defineArtifacts({
  kinds: {
    idea: {
      description: "A researched Roblox game idea the studio can design.",
    },
    design: {
      description: "Core player-facing design for one Roblox game.",
    },
    requirements: {
      description: "Testable requirements the developers build against.",
    },
    build: {
      description: "What was built in the open Roblox Studio place.",
    },
    validation: {
      description: "Acceptance result for the build against the requirements.",
    },
  },
});
