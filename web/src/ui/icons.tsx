import type { SVGProps } from "react";

// Authored 16px icons on one 1.5px stroke (craft floor: drawn icons, never
// glyphs). Decorative by default; pass aria-label to make one meaningful.
type IconProps = SVGProps<SVGSVGElement> & { "aria-label"?: string };

function Icon({ children, "aria-label": label, ...rest }: IconProps & { children: React.ReactNode }) {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden={label ? undefined : true}
      role={label ? "img" : undefined}
      aria-label={label}
      focusable="false"
      {...rest}
    >
      {children}
    </svg>
  );
}

export const IconCheck = (p: IconProps) => <Icon {...p}><path d="M3 8.5 6.5 12 13 4.5" /></Icon>;
export const IconChevronRight = (p: IconProps) => <Icon {...p}><path d="m6 3.5 4.5 4.5L6 12.5" /></Icon>;
export const IconChevronDown = (p: IconProps) => <Icon {...p}><path d="m3.5 6 4.5 4.5L12.5 6" /></Icon>;
export const IconFile = (p: IconProps) => <Icon {...p}><path d="M4 1.75h5L12.25 5v9.25H4z" /><path d="M9 1.75V5h3.25" /></Icon>;
export const IconComment = (p: IconProps) => <Icon {...p}><path d="M2.25 3.25h11.5v7.5H7l-3 2.5v-2.5H2.25z" /></Icon>;
export const IconBranch = (p: IconProps) => <Icon {...p}><circle cx="4.5" cy="3.5" r="1.5" /><circle cx="4.5" cy="12.5" r="1.5" /><circle cx="11.5" cy="5" r="1.5" /><path d="M4.5 5v6M11.5 6.5c0 3-7 2-7 4.5" /></Icon>;
export const IconClose = (p: IconProps) => <Icon {...p}><path d="m4 4 8 8M12 4l-8 8" /></Icon>;
export const IconPaperclip = (p: IconProps) => <Icon {...p}><path d="M13 7.5 8 12.5a3 3 0 0 1-4.25-4.25l5.5-5.5a2 2 0 0 1 2.83 2.83L6.6 11.06a1 1 0 0 1-1.42-1.42L10 4.83" /></Icon>;
export const IconSignOut = (p: IconProps) => <Icon {...p}><path d="M6.5 2.25h-4v11.5h4M10 5l3 3-3 3M13 8H6" /></Icon>;
