import packageJson from "../../package.json";

const currentYear = new Date().getFullYear();

export const APP_CONFIG = {
  name: "CAUTIOUSMEMORY",
  version: packageJson.version,
  copyright: `© ${currentYear}, CAUTIOUSMEMORY.`,
  meta: {
    title: "CAUTIOUSMEMORY — Autonomous Penetration Testing Console",
    description: "LLM-driven Autonomous Penetration Testing System Console",
  },
};
