import type { ReactNode } from 'react';

const paths = {
  copy: <><rect x="8" y="8" width="13" height="13" rx="2" /><path d="M16 8V3H3v13h5" /></>,
  database: <><ellipse cx="12" cy="5" rx="8" ry="3" /><path d="M4 5v14c0 4 16 4 16 0V5M4 12c0 4 16 4 16 0" /></>,
  table: <><rect x="3" y="3" width="18" height="18" rx="2" /><path d="M3 9h18M9 9v12M3 15h18" /></>,
  search: <><circle cx="10.5" cy="10.5" r="6.5" /><path d="m16 16 5 5" /></>,
  plus: <path d="M12 5v14M5 12h14" />,
  close: <path d="m6 6 12 12M6 18 18 6" />,
  refresh: <><path d="M20 8a8 8 0 0 0-14-3L3 8m0-5v5h5M4 16a8 8 0 0 0 14 3l3-3m0 5v-5h-5" /></>,
  sun: <><circle cx="12" cy="12" r="4" /><path d="M12 2v2m0 16v2M2 12h2m16 0h2M5 5l1.5 1.5m11 11L19 19M5 19l1.5-1.5m11-11L19 5" /></>,
  moon: <path d="M20.5 14A9 9 0 0 1 10 3a9 9 0 1 0 10.5 11Z" />,
  terminal: <><path d="m4 6 6 6-6 6m9 0h7" /></>,
  schema: <><rect x="8" y="2" width="8" height="6" rx="1" /><rect x="2" y="16" width="8" height="6" rx="1" /><rect x="14" y="16" width="8" height="6" rx="1" /><path d="M12 8v4M6 16v-4h12v4" /></>,
  folder: <path d="M3 7V5a2 2 0 0 1 2-2h5l2 3h7a2 2 0 0 1 2 2v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z" />,
  'arrow-up-right': <path d="M7 17 17 7M7 7h10v10" />,
  'chevron-right': <path d="m9 5 7 7-7 7" />,
  'chevron-left': <path d="m15 5-7 7 7 7" />,
  'chevron-down': <path d="m5 9 7 7 7-7" />,
  'arrow-up': <path d="M12 20V4m-6 6 6-6 6 6" />,
  'arrow-down': <path d="M12 4v16m-6-6 6 6 6-6" />,
  star: <path d="m12 3 2.8 5.7 6.2.9-4.5 4.4 1.1 6.2L12 17.3l-5.6 2.9 1.1-6.2L3 9.6l6.2-.9Z" />,
  trash: <><path d="M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7m4-7v7" /></>,
  play: <path d="m7 4 14 8-14 8Z" />,
  pause: <><path d="M8 4v16M16 4v16" /></>,
  history: <><path d="M3 11a9 9 0 1 1 2 7M3 4v7h7m2-4v5l3 2" /></>,
  download: <path d="M12 3v12m-5-5 5 5 5-5M4 15v6h16v-6" />,
  key: <><circle cx="8" cy="8" r="5" /><path d="m12 12 9 9m-6-6 3-3m0 6 3-3" /></>,
  menu: <path d="M4 6h16M4 12h16M4 18h16" />,
  help: <><circle cx="12" cy="12" r="9" /><path d="M9 9a3 3 0 1 1 4 2.8c-1 .5-1 1.2-1 2.2m0 3h.01" /></>,
  check: <path d="m5 12 4 4L19 6" />,
  lock: <><rect x="5" y="10" width="14" height="11" rx="2" /><path d="M8 10V7a4 4 0 0 1 8 0v3m-4 5v2" /></>,
} satisfies Record<string, ReactNode>;

export type IconName = keyof typeof paths;

export function Icon({ name, filled = false }: { name: IconName; filled?: boolean }) {
  return (
    <svg className="icon" viewBox="0 0 24 24" fill={filled ? 'currentColor' : 'none'} stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      {paths[name]}
    </svg>
  );
}
