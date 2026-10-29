import { t } from '../i18n'

// Loading shows the loading state.
export default function Loading({ error }: { error?: unknown }) {
  if (error) {
    return (
      <div className="notice danger" style={{ padding: '12px 14px', borderRadius: 8 }}>
        <span style={{ color: 'var(--danger)' }}>{(error as Error).message}</span>
      </div>
    )
  }
  return <p style={{ color: 'var(--muted)' }}>{t('common.loading')}</p>
}
