// Daemon log: the tail of the file, refreshed on demand.

import { useQuery } from '@tanstack/react-query'
import { Button, Group, Text } from '@mantine/core'
import { getLogs } from '../api/client'
import { Page, PageHeader, Panel } from '../components/layout'
import Loading from '../components/Loading'
import { t } from '../i18n'

/** LogsPage shows the tail of the daemon log. */
export default function LogsPage() {
  const { data, isLoading, error, refetch, isFetching } = useQuery({
    queryKey: ['logs'],
    queryFn: () => getLogs(500),
    refetchOnMount: 'always',
  })

  return (
    <Page>
      <PageHeader title={t('logs.title')}>
        <Button variant="default" onClick={() => void refetch()} loading={isFetching}>
          {t('logs.refresh')}
        </Button>
      </PageHeader>

      {isLoading ? (
        <Loading />
      ) : error ? (
        <Loading error={error} />
      ) : (data?.lines.length ?? 0) === 0 ? (
        <Panel>
          <Text c="dimmed">{t('logs.empty')}</Text>
        </Panel>
      ) : (
        <Panel p={0}>
          <Group justify="space-between" px="lg" pt="md">
            <Text size="xs" c="dimmed">
              {t('logs.lines')}: {data?.lines.length}
            </Text>
          </Group>
          <pre className="code-block" style={{ border: 0, borderRadius: 0, background: 'transparent' }}>
            {data?.lines.join('\n')}
          </pre>
        </Panel>
      )}
    </Page>
  )
}
