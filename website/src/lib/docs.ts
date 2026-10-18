import type { CollectionEntry } from "astro:content";

export const docs = (entries: CollectionEntry<"docs">[]) => [...entries].sort((left, right) => left.data.title.localeCompare(right.data.title));
export const slug = (entry: CollectionEntry<"docs">) => {
  const value = entry.id.replace(/\.md$/, "");
  return value === "index" ? "overview" : value;
};
