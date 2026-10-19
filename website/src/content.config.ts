import { defineCollection } from "astro:content";
import { glob } from "astro/loaders";
import { fileURLToPath } from "node:url";
import { z } from "astro/zod";

const docs = defineCollection({
  loader: glob({ pattern: "*.md", base: fileURLToPath(new URL("../../docs/public", import.meta.url)) }),
  schema: z.object({ title: z.string(), description: z.string().optional() }),
});

export const collections = { docs };
