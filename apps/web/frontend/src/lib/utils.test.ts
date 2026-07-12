import { describe, expect, it } from "vitest";
import { chronologicalSections, formatBytes } from "./utils";
import type { Message, Page, Section } from "../types";

describe("formatBytes", () => {
  it("formats bytes and binary units", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1536)).toBe("1.5 KiB");
    expect(formatBytes(2 * 1024 * 1024)).toBe("2.0 MiB");
  });
});

describe("chronologicalSections", () => {
  const message = (timestamp: string): Message => ({
    author_id: "1", author_name: "user", timestamp, edited: false,
    attachments: [], embeds: [], reactions: [], channel_id: "1", channel_name: "general",
  });
  const page = (items: Section[]): Page<Section> => ({ items, next_cursor: "", has_more: false });

  it("prepends older pages and merges a date split across pages", () => {
    const result = chronologicalSections([
      page([{ date: "2026-07-12", messages: [message("12:00"), message("13:00")] }]),
      page([
        { date: "2026-07-11", messages: [message("23:00")] },
        { date: "2026-07-12", messages: [message("00:00")] },
      ]),
    ]);

    expect(result.map(section => section.date)).toEqual(["2026-07-11", "2026-07-12"]);
    expect(result[1].messages.map(item => item.timestamp)).toEqual(["00:00", "12:00", "13:00"]);
  });
});
