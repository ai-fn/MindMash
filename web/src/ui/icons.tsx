// Minimal inline-SVG icon set — only the icons actually consumed by primitives in this
// directory (Dialog/Toast close buttons, Select's chevron, Badge/Toast status marks). Not a
// general-purpose icon system: add icons here only when a primitive needs one.
import type { SVGProps } from 'react'

export type IconProps = SVGProps<SVGSVGElement>

function baseProps(props: IconProps): IconProps {
  return {
    viewBox: '0 0 20 20',
    fill: 'none',
    stroke: 'currentColor',
    strokeWidth: 1.75,
    strokeLinecap: 'round',
    strokeLinejoin: 'round',
    'aria-hidden': true,
    focusable: false,
    width: '1em',
    height: '1em',
    ...props,
  }
}

export function CloseIcon(props: IconProps) {
  return (
    <svg {...baseProps(props)}>
      <path d="M5 5l10 10M15 5L5 15" />
    </svg>
  )
}

export function CheckIcon(props: IconProps) {
  return (
    <svg {...baseProps(props)}>
      <path d="M4 10.5l4 4 8-9" />
    </svg>
  )
}

export function ChevronDownIcon(props: IconProps) {
  return (
    <svg {...baseProps(props)}>
      <path d="M5 7.5l5 5 5-5" />
    </svg>
  )
}

export function AlertIcon(props: IconProps) {
  return (
    <svg {...baseProps(props)}>
      <path d="M10 4v7" />
      <path d="M10 14.2v.1" />
    </svg>
  )
}
