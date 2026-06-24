// Minimal classname combinator — the codebase has no clsx/classnames dependency yet, and
// pulling one in for this alone isn't worth it. Falsy entries (false/null/undefined/'') are
// dropped; everything else is joined with a single space.
export type ClassValue = string | false | null | undefined

export function cn(...values: ClassValue[]): string {
  return values.filter(Boolean).join(' ')
}

// Shared base styling for the text-entry-style form controls (TextInput/Textarea/Select) so
// they stay visually identical — kept here (a non-component module) rather than in
// TextInput.tsx so react-refresh's "only export components" rule doesn't flag it.
export const fieldControlClass = cn(
  'w-full rounded-[var(--radius-md)] border border-border bg-surface px-3 py-2 text-base text-fg',
  'placeholder:text-muted',
  'transition-colors duration-[var(--duration-fast)] ease-default',
  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:border-accent',
  'disabled:cursor-not-allowed disabled:opacity-60',
)
