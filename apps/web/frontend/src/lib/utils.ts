import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";
import type { Page, Section } from "../types";
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
export function formatBytes(size: number) {
  if (size < 1024) return `${size} B`;
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let value = size / 1024,
    i = 0;
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024;
    i++;
  }
  return `${value.toFixed(1)} ${units[i]}`;
}

// Pages arrive newest first, while sections and messages within each page are
// oldest first. Normalize the whole list into chronological order.
export function chronologicalSections(pages: Page<Section>[]): Section[] {
  const sections: Section[] = [];
  for (const page of [...pages].reverse()) {
    for (const section of page.items) {
      const previous = sections.at(-1);
      const messages = [...section.messages];
      if (previous && previous.date === section.date) previous.messages.push(...messages);
      else sections.push({ ...section, messages });
    }
  }
  return sections;
}
