// Side menu: UI sections and navigation between them.
//
// Order and groups mirror the dsh settings menu: the general tab first,
// then models, then connections and service sections.

import { NavLink } from 'react-router-dom'
import { t } from '../i18n'

/** Section icon: shared by the menu and the page headers. */
function Icon({ path }: { path: string }) {
  return (
    <svg
      width={16}
      height={16}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.8}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d={path} />
    </svg>
  )
}

/** Logo: the same drawing as in favicon.svg. */
function Logo() {
  return (
    <svg
      width={22}
      height={22}
      viewBox="0 0 64 64"
      role="img"
      aria-label="cdx"
      style={{ flex: 'none' }}
    >
      <rect width="64" height="64" rx="14" fill="var(--raised)" />
      <path
        d="M14 24a4 4 0 0 1 4-4h9l4 5h15a4 4 0 0 1 4 4v17a4 4 0 0 1-4 4H18a4 4 0 0 1-4-4Z"
        fill="none"
        stroke="var(--accent)"
        strokeWidth={4.5}
        strokeLinejoin="round"
      />
      <path
        d="M28 30.5 L35 37 L28 43.5"
        fill="none"
        stroke="var(--text)"
        strokeWidth={4.5}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}

/** Section icons. Outlines rather than fills, so they read on any background. */
const icons = {
  general: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-2.9 1.2v.2a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-2.9-1.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0-1.2-2.9H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.3-2.9l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 2.9-1.2V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 2.9 1.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0 1.2 2.9H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1Z',
  models: 'M4 7h16 M4 12h16 M4 17h10',
  presets: 'M12 3l8 4.5v9L12 21l-8-4.5v-9Z M12 12l8-4.5 M12 12v9 M12 12 4 7.5',
  sessions: 'M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2Z',
  plugins: 'M9 3v2 M15 3v2 M4 9h2 M4 15h2 M9 21v-2 M15 21v-2 M20 9h-2 M20 15h-2 M9 5h6a4 4 0 0 1 4 4v6a4 4 0 0 1-4 4H9a4 4 0 0 1-4-4V9a4 4 0 0 1 4-4Z',
  mcp: 'M12 2v20 M2 12h20 M5 5l14 14 M19 5 5 19',
  logs: 'M4 4h16v16H4z M8 8h8 M8 12h8 M8 16h5',
}

/** A single menu entry. */
function NavItem({ to, icon, label }: { to: string; icon: keyof typeof icons; label: string }) {
  return (
    <NavLink to={to} className={({ isActive }) => (isActive ? 'nav-item active' : 'nav-item')}>
      <Icon path={icons[icon]} />
      <span>{label}</span>
    </NavLink>
  )
}

/** Sidebar shows the UI sections. */
export default function Sidebar() {
  return (
    <SidebarFrame>
      <NavItem to="/general" icon="general" label={t('nav.general')} />
      <NavItem to="/models" icon="models" label={t('nav.models')} />
      <NavItem to="/presets" icon="presets" label={t('nav.presets')} />

      <div className="nav-gap" />

      <NavItem to="/sessions" icon="sessions" label={t('nav.sessions')} />
      <NavItem to="/plugins" icon="plugins" label={t('nav.plugins')} />
      <NavItem to="/mcp" icon="mcp" label={t('nav.mcp')} />
      <NavItem to="/logs" icon="logs" label={t('nav.logs')} />
    </SidebarFrame>
  )
}

/** SidebarFrame separates the menu markup from the list of entries. */
function SidebarFrame({ children }: { children: React.ReactNode }) {
  return (
    <aside className="sidebar">
      <div className="brand" style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '0 10px 16px' }}>
        <Logo />
        <h4 style={{ margin: 0, color: 'var(--accent-text)', fontSize: 16 }}>cdx</h4>
      </div>
      <nav style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>{children}</nav>
    </aside>
  )
}
