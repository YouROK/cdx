// Page and panel frames.
//
// Page width, spacing and panel look are set once here; pages only
// assemble these building blocks.

import type { ReactNode } from 'react'
import { Card, Container, Group, Title } from '@mantine/core'

/** Width of every page: one value for the whole UI. */
export const PAGE_WIDTH = 'lg'

/**
 * Page frame: the same width and spacing on every page.
 * Each page starts with it and does not build its own Container.
 */
export function Page({ children }: { children: ReactNode }) {
  return (
    <Container size={PAGE_WIDTH} className="page">
      {children}
    </Container>
  )
}

/**
 * Panel: one card look across every section of the UI.
 * Built with withProps so it keeps the polymorphic Card type.
 */
export const Panel = Card.withProps({ withBorder: true, p: 'lg' })

/** Stat tile: caption on top, value below. */
export const StatCard = Card.withProps({
  withBorder: true,
  className: 'stat-card',
})

/**
 * Page header: title on the left, actions on the right.
 * The same header look on every page.
 */
export function PageHeader({
  title,
  children,
}: {
  title: ReactNode
  children?: ReactNode
}) {
  return (
    <Group justify="space-between" mb="md">
      {typeof title === 'string' ? <Title order={3}>{title}</Title> : title}
      {children && <Group gap="xs">{children}</Group>}
    </Group>
  )
}

/** Section title inside a panel: 16px with space below. */
export function SectionTitle({ children }: { children: ReactNode }) {
  return (
    <Title order={4} mb="md">
      {children}
    </Title>
  )
}
