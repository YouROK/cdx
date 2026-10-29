// Hint icon: a circled icon with popup text on hover.

import { Tooltip } from '@mantine/core'

/**
 * HintIcon shows an icon with an explanation on hover.
 * It carries its own styling: the icon appears in many places and must
 * look the same everywhere.
 */
export function HintIcon({
  label,
  size = 16,
  position = 'top',
}: {
  label: string
  size?: number
  position?: 'top' | 'bottom' | 'left' | 'right'
}) {
  return (
    <Tooltip label={label} position={position} withArrow multiline w={320} openDelay={150}>
      <span className="hint-icon" role="img" aria-label={label} tabIndex={0}>
        <svg width={size} height={size} viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <circle cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="2" />
          <path
            d="M9.2 9.2a2.9 2.9 0 1 1 4.2 2.6c-.9.5-1.4 1-1.4 2"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
          />
          <circle cx="12" cy="17.2" r="1.2" fill="currentColor" />
        </svg>
      </span>
    </Tooltip>
  )
}
