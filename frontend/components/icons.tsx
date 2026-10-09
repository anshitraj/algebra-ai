// One authored icon set: 24px grid, 1.6 stroke, round caps/joins,
// currentColor. Brand marks (Google, GitHub) keep their own geometry.

import type { SVGProps } from "react";

type IconProps = SVGProps<SVGSVGElement> & { size?: number };

function Svg({ size = 18, children, ...props }: IconProps & { children: React.ReactNode }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.6}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      {...props}
    >
      {children}
    </svg>
  );
}

export const IconArrowRight = (p: IconProps) => (
  <Svg {...p}>
    <path d="M5 12h14M13 6l6 6-6 6" />
  </Svg>
);
export const IconArrowLeft = (p: IconProps) => (
  <Svg {...p}>
    <path d="M19 12H5M11 18l-6-6 6-6" />
  </Svg>
);
export const IconArrowUp = (p: IconProps) => (
  <Svg {...p}>
    <path d="M12 19V5M6 11l6-6 6 6" />
  </Svg>
);
export const IconCheck = (p: IconProps) => (
  <Svg {...p}>
    <path d="M4.5 12.5l5 5L19.5 7" />
  </Svg>
);
export const IconX = (p: IconProps) => (
  <Svg {...p}>
    <path d="M6 6l12 12M18 6L6 18" />
  </Svg>
);
export const IconChevronDown = (p: IconProps) => (
  <Svg {...p}>
    <path d="M6 9l6 6 6-6" />
  </Svg>
);
export const IconEye = (p: IconProps) => (
  <Svg {...p}>
    <path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12z" />
    <circle cx="12" cy="12" r="3" />
  </Svg>
);
export const IconEyeOff = (p: IconProps) => (
  <Svg {...p}>
    <path d="M3 3l18 18M10.6 5.6A9.8 9.8 0 0112 5.5c6 0 9.5 6.5 9.5 6.5a17 17 0 01-3.1 3.9M6.6 6.7C3.9 8.4 2.5 12 2.5 12S6 18.5 12 18.5a9.5 9.5 0 004.4-1.1" />
    <path d="M9.9 9.9a3 3 0 004.2 4.2" />
  </Svg>
);
export const IconMail = (p: IconProps) => (
  <Svg {...p}>
    <rect x="3" y="5" width="18" height="14" rx="2.5" />
    <path d="M3.5 7l8.5 6 8.5-6" />
  </Svg>
);
export const IconLock = (p: IconProps) => (
  <Svg {...p}>
    <rect x="4.5" y="10.5" width="15" height="10" rx="2.5" />
    <path d="M8 10.5V7.5a4 4 0 018 0v3" />
  </Svg>
);
export const IconShield = (p: IconProps) => (
  <Svg {...p}>
    <path d="M12 3l7.5 3v5.5c0 4.6-3.2 8.3-7.5 9.5-4.3-1.2-7.5-4.9-7.5-9.5V6z" />
    <path d="M8.8 12l2.2 2.2 4.3-4.4" />
  </Svg>
);
export const IconTag = (p: IconProps) => (
  <Svg {...p}>
    <path d="M3.5 12.4V4.5a1 1 0 011-1h7.9a1 1 0 01.7.3l8 8a1 1 0 010 1.4l-7.9 7.9a1 1 0 01-1.4 0l-8-8a1 1 0 01-.3-.7z" />
    <circle cx="8" cy="8" r="1.4" />
  </Svg>
);
export const IconBan = (p: IconProps) => (
  <Svg {...p}>
    <circle cx="12" cy="12" r="8.5" />
    <path d="M6 6l12 12" />
  </Svg>
);
export const IconGauge = (p: IconProps) => (
  <Svg {...p}>
    <path d="M4.5 17.5a8.5 8.5 0 1115 0" />
    <path d="M12 13.5l4-4.5" />
    <circle cx="12" cy="13.5" r="1.3" />
  </Svg>
);
export const IconLogOut = (p: IconProps) => (
  <Svg {...p}>
    <path d="M14.5 4.5H18a1.5 1.5 0 011.5 1.5v12a1.5 1.5 0 01-1.5 1.5h-3.5M10 16.5L5.5 12 10 7.5M5.5 12h10" />
  </Svg>
);
export const IconSettings = (p: IconProps) => (
  <Svg {...p}>
    <circle cx="12" cy="12" r="3" />
    <path d="M19.4 14.6a1.4 1.4 0 00.3 1.5l.1.1a1.7 1.7 0 11-2.4 2.4l-.1-.1a1.4 1.4 0 00-1.5-.3 1.4 1.4 0 00-.9 1.3v.2a1.7 1.7 0 01-3.4 0V19.6a1.4 1.4 0 00-.9-1.3 1.4 1.4 0 00-1.5.3l-.1.1a1.7 1.7 0 11-2.4-2.4l.1-.1a1.4 1.4 0 00.3-1.5 1.4 1.4 0 00-1.3-.9h-.2a1.7 1.7 0 010-3.4h.1a1.4 1.4 0 001.3-.9 1.4 1.4 0 00-.3-1.5l-.1-.1a1.7 1.7 0 112.4-2.4l.1.1a1.4 1.4 0 001.5.3h.1a1.4 1.4 0 00.8-1.3v-.2a1.7 1.7 0 013.4 0v.1a1.4 1.4 0 00.9 1.3 1.4 1.4 0 001.5-.3l.1-.1a1.7 1.7 0 112.4 2.4l-.1.1a1.4 1.4 0 00-.3 1.5v.1a1.4 1.4 0 001.3.8h.2a1.7 1.7 0 010 3.4h-.1a1.4 1.4 0 00-1.3.9z" />
  </Svg>
);
export const IconGrid = (p: IconProps) => (
  <Svg {...p}>
    <rect x="3.5" y="3.5" width="7" height="7" rx="1.8" />
    <rect x="13.5" y="3.5" width="7" height="7" rx="1.8" />
    <rect x="3.5" y="13.5" width="7" height="7" rx="1.8" />
    <rect x="13.5" y="13.5" width="7" height="7" rx="1.8" />
  </Svg>
);
export const IconChat = (p: IconProps) => (
  <Svg {...p}>
    <path d="M20.5 11.5a8 8 0 01-11.6 7.1L4 20l1.4-4.4a8 8 0 1115.1-4.1z" />
    <path d="M8.5 11.5h.01M12 11.5h.01M15.5 11.5h.01" strokeWidth={2.2} />
  </Svg>
);
export const IconInbox = (p: IconProps) => (
  <Svg {...p}>
    <path d="M3.5 13.5l2.6-7.3A1.5 1.5 0 017.5 5h9a1.5 1.5 0 011.4 1.2l2.6 7.3V18a1.5 1.5 0 01-1.5 1.5H5A1.5 1.5 0 013.5 18z" />
    <path d="M3.5 13.5h4.5l1.5 2.5h5l1.5-2.5h4.5" />
  </Svg>
);
export const IconWallet = (p: IconProps) => (
  <Svg {...p}>
    <path d="M4 7.5A2.5 2.5 0 016.5 5h11A1.5 1.5 0 0119 6.5V8" />
    <rect x="4" y="8" width="16.5" height="11.5" rx="2.5" />
    <circle cx="16.3" cy="13.8" r="1.2" />
  </Svg>
);
export const IconPlug = (p: IconProps) => (
  <Svg {...p}>
    <path d="M9 3.5V7M15 3.5V7M6.5 7h11v3.5a5.5 5.5 0 01-11 0V7zM12 16v4.5" />
  </Svg>
);
export const IconStore = (p: IconProps) => (
  <Svg {...p}>
    <path d="M4 9.5V20h16V9.5M3 9.5l1.8-5h14.4l1.8 5a2.8 2.8 0 01-5.4 1 2.8 2.8 0 01-5.2 0 2.8 2.8 0 01-5.4-1z" />
    <path d="M9.5 20v-5h5v5" />
  </Svg>
);
export const IconPlus = (p: IconProps) => (
  <Svg {...p}>
    <path d="M12 5v14M5 12h14" />
  </Svg>
);
export const IconMenu = (p: IconProps) => (
  <Svg {...p}>
    <path d="M4 7h16M4 12h16M4 17h16" />
  </Svg>
);
export const IconSearch = (p: IconProps) => (
  <Svg {...p}>
    <circle cx="11" cy="11" r="6.5" />
    <path d="M20 20l-4.3-4.3" />
  </Svg>
);
export const IconReceipt = (p: IconProps) => (
  <Svg {...p}>
    <path d="M6 3.5h12v17l-2.4-1.6-2.4 1.6-1.2-.8-1.2.8-2.4-1.6L6 20.5z" />
    <path d="M9 8.5h6M9 12h6M9 15.5h3.5" />
  </Svg>
);
export const IconClock = (p: IconProps) => (
  <Svg {...p}>
    <circle cx="12" cy="12" r="8.5" />
    <path d="M12 7.5V12l3 2" />
  </Svg>
);
export const IconList = (p: IconProps) => (
  <Svg {...p}>
    <path d="M9 6.5h11M9 12h11M9 17.5h11M4.5 6.5h.01M4.5 12h.01M4.5 17.5h.01" />
  </Svg>
);
export const IconStop = (p: IconProps) => (
  <Svg {...p}>
    <rect x="7" y="7" width="10" height="10" rx="1.8" fill="currentColor" stroke="none" />
  </Svg>
);
export const IconRefresh = (p: IconProps) => (
  <Svg {...p}>
    <path d="M20 11.5A8 8 0 006.1 6.3L4 8.5M4 4v4.5h4.5M4 12.5a8 8 0 0013.9 5.2l2.1-2.2M20 20v-4.5h-4.5" />
  </Svg>
);
export const IconExternal = (p: IconProps) => (
  <Svg {...p}>
    <path d="M13.5 4.5h6v6M19.5 4.5L11 13M17.5 14v4a1.5 1.5 0 01-1.5 1.5H6A1.5 1.5 0 014.5 18V8A1.5 1.5 0 016 6.5h4" />
  </Svg>
);
export function Spinner({ size = 16, className = "" }: { size?: number; className?: string }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" className={`animate-spin ${className}`} aria-hidden="true">
      <circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" strokeOpacity="0.2" strokeWidth="2.5" />
      <path d="M21 12a9 9 0 00-9-9" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
    </svg>
  );
}

export function GoogleMark({ size = 18 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 48 48" aria-hidden="true">
      <path fill="#FFC107" d="M43.6 20.5H42V20H24v8h11.3C33.7 32.7 29.2 36 24 36c-6.6 0-12-5.4-12-12s5.4-12 12-12c3.1 0 5.8 1.2 7.9 3.1l5.7-5.7C34 6.1 29.3 4 24 4 12.9 4 4 12.9 4 24s8.9 20 20 20 20-8.9 20-20c0-1.3-.1-2.4-.4-3.5z" />
      <path fill="#FF3D00" d="M6.3 14.7l6.6 4.8C14.7 15.1 19 12 24 12c3.1 0 5.8 1.2 7.9 3.1l5.7-5.7C34 6.1 29.3 4 24 4 16.3 4 9.7 8.3 6.3 14.7z" />
      <path fill="#4CAF50" d="M24 44c5.2 0 9.9-2 13.4-5.2l-6.2-5.2C29.2 35.1 26.7 36 24 36c-5.2 0-9.6-3.3-11.3-7.9l-6.5 5C9.5 39.6 16.2 44 24 44z" />
      <path fill="#1976D2" d="M43.6 20.5H42V20H24v8h11.3c-.8 2.2-2.2 4.2-4.1 5.6l6.2 5.2C37 39.2 44 34 44 24c0-1.3-.1-2.4-.4-3.5z" />
    </svg>
  );
}

export function GitHubMark({ size = 18 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d="M12 .5C5.7.5.5 5.7.5 12a11.5 11.5 0 007.9 10.9c.6.1.8-.3.8-.6v-2c-3.2.7-3.9-1.5-3.9-1.5-.5-1.3-1.3-1.7-1.3-1.7-1-.7.1-.7.1-.7 1.2.1 1.8 1.2 1.8 1.2 1 1.8 2.8 1.3 3.5 1 .1-.8.4-1.3.8-1.6-2.6-.3-5.3-1.3-5.3-5.7 0-1.3.5-2.3 1.2-3.1-.1-.3-.5-1.5.1-3.1 0 0 1-.3 3.2 1.2a11 11 0 015.8 0c2.2-1.5 3.2-1.2 3.2-1.2.6 1.6.2 2.8.1 3.1.7.8 1.2 1.8 1.2 3.1 0 4.4-2.7 5.4-5.3 5.7.4.4.8 1.1.8 2.2v3.3c0 .3.2.7.8.6A11.5 11.5 0 0023.5 12C23.5 5.7 18.3.5 12 .5z" />
    </svg>
  );
}
