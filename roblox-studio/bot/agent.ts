import { defineAgent } from "@cursor/bdk";

export default defineAgent({
  name: "Roblox Studio",
  description:
    "A Roblox game studio. Researchers, designers, product owners, developers, and validators share one board, build in the open Studio session, and release only after a playtest passes.",
  model: {
    id: "grok-4.5",
    params: [
      { id: "effort", value: "high" },
      { id: "fast", value: "true" },
    ],
  },
  hosting: {
    egressDomains: ["apis.roblox.com"],
    secretNames: ["ROBLOX_OPEN_CLOUD_API_KEY"],
  },
});
