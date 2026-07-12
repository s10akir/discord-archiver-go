export type Channel = {
  id: string;
  name: string;
  parent_id?: string;
  is_thread: boolean;
  has_link: boolean;
  threads?: Channel[];
};
export type Group = {
  id: string;
  name: string;
  uncategorized: boolean;
  items: Channel[];
};
export type Navigation = { guild_id: string; groups: Group[] };
export type Attachment = {
  url?: string;
  filename: string;
  size: number;
  width?: number;
  height?: number;
  is_image: boolean;
  is_video: boolean;
  is_audio: boolean;
  available: boolean;
};
export type Embed = {
  title?: string;
  url?: string;
  description?: string;
  image_url?: string;
  image_width?: number;
  image_height?: number;
  color: string;
};
export type Message = {
  author_id: string;
  author_name: string;
  avatar_url?: string;
  timestamp: string;
  edited: boolean;
  content_html?: string;
  reply_snippet?: string;
  attachments: Attachment[];
  embeds: Embed[];
  reactions: { emoji: string; count: number }[];
  channel_id: string;
  channel_name: string;
};
export type Section = { date: string; messages: Message[] };
export type Page<T> = { items: T[]; next_cursor: string; has_more: boolean };
export type MediaItem = {
  author_name: string;
  timestamp: string;
  channel_id?: string;
  channel_name?: string;
  attachment?: Attachment;
  embed?: Embed;
};
export type Option = { value: string; label: string };
