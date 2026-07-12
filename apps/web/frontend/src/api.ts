import type { MediaItem, Navigation, Option, Page, Section } from "./types";

async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  const response = await fetch(path, { signal });
  if (!response.ok) throw new Error(`${response.status}`);
  return response.json();
}

function normalizeSections(page: Page<Section>): Page<Section> {
  return {
    ...page,
    items: (page.items ?? []).map((section) => ({
      ...section,
      messages: (section.messages ?? []).map((message) => ({
        ...message,
        attachments: message.attachments ?? [],
        embeds: message.embeds ?? [],
        reactions: message.reactions ?? [],
      })),
    })),
  };
}

export const api = {
  guilds: (signal?: AbortSignal) => get<{ guilds: string[] }>("/api/v1/guilds", signal),
  navigation: (guild: string, signal?: AbortSignal) =>
    get<Navigation>(`/api/v1/guilds/${encodeURIComponent(guild)}/navigation`, signal),
  messages: (guild: string, channel: string, before = "", signal?: AbortSignal) =>
    get<Page<Section>>(
      `/api/v1/guilds/${encodeURIComponent(guild)}/messages?${new URLSearchParams({ ...(channel && { channel }), ...(before && { before }) })}`,
      signal,
    ).then(normalizeSections),
  media: (guild: string, kind: string, channel: string, before = "", signal?: AbortSignal) =>
    get<Page<MediaItem>>(
      `/api/v1/guilds/${encodeURIComponent(guild)}/media/${kind}?${new URLSearchParams({ ...(channel && { channel }), ...(before && { before }) })}`,
      signal,
    ),
  searchOptions: (signal?: AbortSignal) =>
    get<{ channels: Option[]; authors: Option[] }>("/api/v1/search/options", signal),
  search: (params: URLSearchParams, before = "", signal?: AbortSignal) => {
    const query = new URLSearchParams(params);
    if (before) query.set("before", before);
    return get<Page<Section>>(`/api/v1/search/messages?${query}`, signal).then(normalizeSections);
  },
};
