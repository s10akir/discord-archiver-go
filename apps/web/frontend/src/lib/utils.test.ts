import { describe, expect, it } from "vitest";
import { formatBytes, newestFirstSections } from "./utils";
import type { Message, Page, Section } from "../types";

describe("formatBytes", () => {
  it("formats bytes and binary units", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1536)).toBe("1.5 KiB");
    expect(formatBytes(2 * 1024 * 1024)).toBe("2.0 MiB");
  });
});

describe("newestFirstSections", () => {
  const message = (timestamp: string): Message => ({
    author_id: "1", author_name: "user", timestamp, edited: false,
    attachments: [], embeds: [], reactions: [], channel_id: "1", channel_name: "general",
  });
  const page = (items: Section[]): Page<Section> => ({ items, next_cursor: "", has_more: false });

  it("appends older pages below and merges a date split across pages", () => {
    const result = newestFirstSections([
      page([{ date: "2026-07-12", messages: [message("12:00"), message("13:00")] }]),
      page([
        { date: "2026-07-11", messages: [message("23:00")] },
        { date: "2026-07-12", messages: [message("00:00")] },
      ]),
    ]);

    expect(result.map(section => section.date)).toEqual(["2026-07-12", "2026-07-11"]);
    expect(result[0].messages.map(item => item.timestamp)).toEqual(["13:00", "12:00", "00:00"]);
  });
});
