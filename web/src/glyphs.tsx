/* One line weight, one corner style: every glyph is drawn on a 20px grid with 1.5px strokes. */
const paths = {
  board: 'M3.5 5.5a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2h-9a2 2 0 0 1-2-2zM7 8h6M7 12h4',
  plus: 'M10 4.5v11M4.5 10h11',
  list: 'M7.5 6h8.5M7.5 10h8.5M7.5 14h8.5M4.7 6a.7.7 0 1 1-1.4 0 .7.7 0 0 1 1.4 0zM4.7 10a.7.7 0 1 1-1.4 0 .7.7 0 0 1 1.4 0zM4.7 14a.7.7 0 1 1-1.4 0 .7.7 0 0 1 1.4 0z',
  cards: 'M3.5 4.5h5.5v5h-5.5zM11 4.5h5.5v5H11zM3.5 11.5h5.5v4h-5.5zM11 11.5h5.5v4H11z',
  chevron: 'M8 5.5l4.5 4.5L8 14.5',
  back: 'M12 5.5L7.5 10l4.5 4.5',
  more: 'M5.7 10a.7.7 0 1 1-1.4 0 .7.7 0 0 1 1.4 0zM10.7 10a.7.7 0 1 1-1.4 0 .7.7 0 0 1 1.4 0zM15.7 10a.7.7 0 1 1-1.4 0 .7.7 0 0 1 1.4 0z',
  bell: 'M6 8.5a4 4 0 0 1 8 0c0 3.5 1.5 5 1.5 5h-11S6 12 6 8.5zM8.5 16a1.6 1.6 0 0 0 3 0',
  lan: 'M12 5a2 2 0 1 1-4 0 2 2 0 0 1 4 0zM7 15a2 2 0 1 1-4 0 2 2 0 0 1 4 0zM17 15a2 2 0 1 1-4 0 2 2 0 0 1 4 0zM9 6.8l-3 6.4M11 6.8l3 6.4',
  settings: 'M4 6h7M14 6h2M4 14h2M9 14h7M12.5 4.5v3M7.5 12.5v3',
  search: 'M9 15a6 6 0 1 0 0-12 6 6 0 0 0 0 12zM13.5 13.5L17 17',
  sun: 'M10 13.5a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7zM10 2.5v1.5M10 16v1.5M2.5 10H4M16 10h1.5M4.7 4.7l1 1M14.3 14.3l1 1M4.7 15.3l1-1M14.3 5.7l1-1',
  moon: 'M15.5 12.5A6.5 6.5 0 0 1 7.5 4.5a6.5 6.5 0 1 0 8 8z',
  check: 'M4.5 10.5l3.5 3.5 7.5-8',
  copy: 'M7.5 7.5h8v8h-8zM12.5 7.5v-3h-8v8h3',
  trash: 'M4.5 6h11M8.5 6V4.5h3V6M6 6l.7 9.5h6.6L14 6',
  edit: 'M12.5 4.5l3 3-8 8H4.5v-3zM11 6l3 3',
  sparkle: 'M10 3.5l1.6 4.9 4.9 1.6-4.9 1.6L10 16.5l-1.6-4.9L3.5 10l4.9-1.6z',
  terminal: 'M3.5 4.5h13v11h-13zM6.5 8l2.5 2-2.5 2M10.5 12.5h3',
  globe:
    'M10 16.5a6.5 6.5 0 1 0 0-13 6.5 6.5 0 0 0 0 13zM3.5 10h13M10 3.5c-2 2.2-2 10.8 0 13M10 3.5c2 2.2 2 10.8 0 13',
  plug: 'M7.5 3.5v3M12.5 3.5v3M5.5 6.5h9v2.5a4.5 4.5 0 0 1-9 0zM10 13.5v3',
  process:
    'M6 6h8v8H6zM8.5 3.5V6M11.5 3.5V6M8.5 14v2.5M11.5 14v2.5M3.5 8.5H6M3.5 11.5H6M14 8.5h2.5M14 11.5h2.5',
  service: 'M10 3.5l6.5 3.5-6.5 3.5L3.5 7zM3.5 10.5l6.5 3.5 6.5-3.5M3.5 13.5l6.5 3.5 6.5-3.5',
  task: 'M10 16.5a6.5 6.5 0 1 0 0-13 6.5 6.5 0 0 0 0 13zM10 6.5V10l2.5 1.5',
  other: 'M10 16.5a6.5 6.5 0 1 0 0-13 6.5 6.5 0 0 0 0 13zM10.7 10a.7.7 0 1 1-1.4 0 .7.7 0 0 1 1.4 0z',
  external: 'M8.5 4.5h-4v11h11v-4M11.5 4.5h4v4M15.5 4.5L9 11',
  close: 'M5.5 5.5l9 9M14.5 5.5l-9 9',
  key: 'M10 13a3 3 0 1 1-6 0 3 3 0 0 1 6 0zM9.2 10.8L16 4M13.5 6.5l2 2M12 8l1.5 1.5',
  refresh: 'M15.5 8A6 6 0 0 0 4.6 6.5M4.5 12a6 6 0 0 0 10.9 1.5M15.5 4v4h-4M4.5 16v-4h4',
  menu: 'M3.5 6h13M3.5 10h13M3.5 14h13',
  upload: 'M10 13V4.5M6.5 8L10 4.5 13.5 8M4.5 13v2.5h11V13',
  computer: 'M3.5 4.5h13v9h-13zM7.5 16.5h5M10 13.5v3',
  eye: 'M2.5 10s2.7-5 7.5-5 7.5 5 7.5 5-2.7 5-7.5 5-7.5-5-7.5-5zM10 12a2 2 0 1 0 0-4 2 2 0 0 0 0 4z',
  lock: 'M5.5 9h9v7.5h-9zM7.5 9V6.5a2.5 2.5 0 0 1 5 0V9',
  bolt: 'M11 3.5L5.5 11H10l-1 5.5L14.5 9H10z',
  agent:
    'M10 3.5v2M6 7.5h8a2 2 0 0 1 2 2v4a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2v-4a2 2 0 0 1 2-2zM8.7 11.5a.7.7 0 1 1-1.4 0 .7.7 0 0 1 1.4 0zM12.7 11.5a.7.7 0 1 1-1.4 0 .7.7 0 0 1 1.4 0z',
  inbox: 'M3.5 11.5l2-6.5h9l2 6.5v4h-13zM3.5 11.5h4l1 2h3l1-2h4',
  // Service roles in the dependency view.
  database:
    'M4.5 5.5c0-1.1 2.5-2 5.5-2s5.5.9 5.5 2-2.5 2-5.5 2-5.5-.9-5.5-2zM4.5 5.5v9c0 1.1 2.5 2 5.5 2s5.5-.9 5.5-2v-9M4.5 10c0 1.1 2.5 2 5.5 2s5.5-.9 5.5-2',
  api: 'M7 6l-3.5 4L7 14M13 6l3.5 4-3.5 4M11 4.5l-2 11',
  app: 'M3.5 4.5h13v11h-13zM3.5 7.5h13M5.7 6a.2.2 0 1 1 0 .01M7.7 6a.2.2 0 1 1 0 .01',
  queue: 'M3.5 5.5h13M3.5 10h13M3.5 14.5h8M14 12.5l2.5 2-2.5 2',
  storage: 'M3.5 6.5h13v3h-13zM3.5 9.5h13v4h-13zM3.5 13.5h13v2.5h-13zM13.5 8h.01M13.5 11.5h.01',
  tunnel: 'M3.5 16.5V10a6.5 6.5 0 0 1 13 0v6.5M7 16.5V10a3 3 0 0 1 6 0v6.5M2.5 16.5h15',
  gateway: 'M10 3.5l6 2.5v4c0 3.5-2.6 5.8-6 6.5-3.4-.7-6-3-6-6.5V6zM7.5 10h5M10.5 8l2 2-2 2',
  worker:
    'M10 12.5a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5zM10 3v2M10 15v2M3 10h2M15 10h2M5 5l1.4 1.4M13.6 13.6L15 15M5 15l1.4-1.4M13.6 6.4L15 5',
  graph:
    'M6 6.5a2 2 0 1 0 0-4 2 2 0 0 0 0 4zM14 6.5a2 2 0 1 0 0-4 2 2 0 0 0 0 4zM10 17.5a2 2 0 1 0 0-4 2 2 0 0 0 0 4zM6 6.5l3.2 7.2M14 6.5l-3.2 7.2',
  doc: 'M5.5 3.5h6l3.5 3.5v9.5h-9.5zM11.5 3.5V7H15M8 10.5h4.5M8 13.5h4.5',
} as const

export type GlyphName = keyof typeof paths

export function Glyph({
  name,
  size = 18,
  className,
}: {
  name: GlyphName
  size?: number
  className?: string
}) {
  return (
    <svg
      className={'lm-glyph' + (className ? ' ' + className : '')}
      viewBox="0 0 20 20"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.5}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d={paths[name]} />
    </svg>
  )
}

export const kindGlyph: Record<string, GlyphName> = {
  port: 'plug',
  url: 'globe',
  service: 'service',
  process: 'process',
  task: 'task',
  job: 'task',
  other: 'other',
}

export const kindName: Record<string, string> = {
  port: '端口',
  url: '网址',
  service: 'Windows 服务',
  process: '进程',
  task: '计划任务',
  job: '批处理任务',
  other: '自定义',
}
