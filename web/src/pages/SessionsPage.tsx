// Sessions: the list and the history. Read-only, requests go through the CLI.

import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Badge, Button, Group, Stack, Table, Text, Title } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { cancelRunning, getRunning, getSessions, type RunningView } from '../api/client'
import { Page, PageHeader, Panel } from '../components/layout'
import Loading from '../components/Loading'
import { t } from '../i18n'
import { formatDateTime } from '../utils/format'

/** SessionsPage shows running turns and the session list. */
export default function SessionsPage() {
  const queryClient = useQueryClient()
  const { data, isLoading, error } = useQuery({ queryKey: ['sessions'], queryFn: getSessions })

  // Running turns refresh on their own: otherwise the list would only
  // show what was there when the page opened.
  const running = useQuery({
    queryKey: ['running'],
    queryFn: getRunning,
    refetchInterval: 2000,
  })

  const cancel = useMutation({
    mutationFn: (id: string) => cancelRunning(id),
    onSuccess: () => {
      notifications.show({ message: t('running.canceled'), color: 'teal', withBorder: true })
      void queryClient.invalidateQueries({ queryKey: ['running'] })
    },
    onError: (err: unknown) =>
      notifications.show({ message: (err as Error).message, color: 'red', withBorder: true }),
  })

  if (isLoading) return <Page><Loading /></Page>
  if (error) return <Page><Loading error={error} /></Page>

  const sessions = data?.sessions ?? []

  return (
    <Page>
      <PageHeader title={t('sessions.title')} />
      <Text size="sm" c="dimmed" mb="md">
        {t('sessions.intro')}
      </Text>

      <RunningPanel
        turns={running.data?.turns ?? []}
        onCancel={(id) => cancel.mutate(id)}
        canceling={cancel.isPending}
      />

      <Panel p={0}>
        {sessions.length === 0 ? (
          <Text c="dimmed" p="lg">
            {t('sessions.empty')}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={640}>
            <Table highlightOnHover verticalSpacing="sm" horizontalSpacing="md">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t('sessions.title_col')}</Table.Th>
                  <Table.Th>{t('sessions.root')}</Table.Th>
                  <Table.Th>{t('sessions.messages')}</Table.Th>
                  <Table.Th>{t('sessions.updated')}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {sessions.map((s) => (
                  <Table.Tr key={s.id}>
                    <Table.Td>
                      <Stack gap={2}>
                        <Group gap="xs">
                          <Link to={`/sessions/${s.id}`}>{s.title || s.id}</Link>
                          {s.active && (
                            <Badge color="teal" variant="light">
                              {t('sessions.active')}
                            </Badge>
                          )}
                          {s.inherited && (
                            <Badge color="gray" variant="light">
                              {t('sessions.inherited')}
                            </Badge>
                          )}
                        </Group>
                        <Text size="xs" c="dimmed" className="mono">
                          {s.id}
                        </Text>
                      </Stack>
                    </Table.Td>
                    <Table.Td>
                      <Text size="xs" c="dimmed" className="mono">
                        {s.root}
                      </Text>
                    </Table.Td>
                    <Table.Td>{s.messages}</Table.Td>
                    <Table.Td>
                      <Text size="sm" c="dimmed">
                        {formatDateTime(s.updated_at)}
                      </Text>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Panel>
    </Page>
  )
}

/** RunningPanel shows running turns with a cancel button. */
function RunningPanel({
  turns,
  onCancel,
  canceling,
}: {
  turns: RunningView[]
  onCancel: (id: string) => void
  canceling: boolean
}) {
  if (turns.length === 0) return null

  return (
    <Panel mb="md">
      <Group gap="xs" mb="sm">
        <Title order={4} m={0}>
          {t('running.title')}
        </Title>
        <Badge color="brand" variant="light">
          {turns.length}
        </Badge>
      </Group>
      <Text size="xs" c="dimmed" mb="md">
        {t('running.hint')}
      </Text>

      <Stack gap="xs">
        {turns.map((turn) => (
          <div className="model-row" key={turn.id}>
            <Group justify="space-between" wrap="nowrap" align="flex-start">
              <Stack gap={2} style={{ minWidth: 0 }}>
                <Group gap="xs">
                  <Badge color="brand" variant="light">
                    {turn.preset}
                  </Badge>
                  <Text size="xs" c="dimmed" className="mono">
                    {turn.model || '-'}
                  </Text>
                  <Text size="xs" c="dimmed">
                    {formatDateTime(turn.started)}
                  </Text>
                </Group>
                <Text size="sm" style={{ wordBreak: 'break-word' }}>
                  {turn.prompt}
                </Text>
                <Text size="xs" c="dimmed" className="mono">
                  {turn.cwd}
                </Text>
              </Stack>
              <Button
                variant="light"
                color="red"
                size="xs"
                onClick={() => onCancel(turn.id)}
                loading={canceling}
              >
                {t('running.cancel')}
              </Button>
            </Group>
          </div>
        ))}
      </Stack>
    </Panel>
  )
}
