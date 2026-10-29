// One session history: entries in order, summaries and tool calls.

import { useQuery } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { Anchor, Badge, Group, Stack, Text, Title } from '@mantine/core'
import { getSession, type MessageView } from '../api/client'
import { Page, Panel } from '../components/layout'
import Loading from '../components/Loading'
import { t } from '../i18n'
import { formatDateTime } from '../utils/format'

/** SessionPage shows the history of the selected session. */
export default function SessionPage() {
  const { id } = useParams<{ id: string }>()
  const { data, isLoading, error } = useQuery({
    queryKey: ['session', id],
    queryFn: () => getSession(id!),
    enabled: Boolean(id),
  })

  if (isLoading) return <Page><Loading /></Page>
  if (error) return <Page><Loading error={error} /></Page>
  if (!data) return null

  return (
    <Page>
      <Anchor component={Link} to="/sessions" size="sm">
        &larr; {t('sessions.back')}
      </Anchor>

      <Group justify="space-between" align="flex-start" mt="sm" mb="md">
        <Stack gap={2}>
          <Title order={3}>{data.session.title || data.session.id}</Title>
          <Text size="xs" c="dimmed" className="mono">
            {data.session.root}
          </Text>
        </Stack>
        <Stack gap={2} align="flex-end">
          <Text size="xs" c="dimmed" className="mono">
            {data.session.id}
          </Text>
          <Text size="xs" c="dimmed">
            {formatDateTime(data.session.updated_at)}
          </Text>
        </Stack>
      </Group>

      <Panel>
        <Stack gap="md">
          {data.messages.length === 0 ? (
            <Text c="dimmed">{t('session.noMessages')}</Text>
          ) : (
            data.messages.map((msg) => <MessageBlock key={msg.seq} message={msg} />)
          )}
        </Stack>
      </Panel>
    </Page>
  )
}

/** MessageBlock shows one history entry. */
function MessageBlock({ message }: { message: MessageView }) {
  const meta = label(message)

  return (
    <div>
      <Group gap="xs" mb={4}>
        <Badge color={colorOf(message.kind)} variant="light">
          {meta}
        </Badge>
        {message.tokens_in > 0 && (
          <Text size="xs" c="dimmed">
            {t('session.tokens')}: {message.tokens_in}/{message.tokens_out}
          </Text>
        )}
        {message.ts > 0 && (
          <Text size="xs" c="dimmed">
            {formatDateTime(message.ts)}
          </Text>
        )}
      </Group>
      <div className="code-block">{message.content || '-'}</div>
    </div>
  )
}

function label(msg: MessageView): string {
  if (msg.kind === 'summary') {
    return `${t('session.summary')} ${msg.covers_from}-${msg.covers_to}`
  }
  if (msg.kind === 'tool_call' || msg.kind === 'tool_result') {
    return `${msg.kind}: ${msg.tool_name}`
  }
  return msg.kind
}

function colorOf(kind: string): string {
  switch (kind) {
    case 'user':
      return 'brand'
    case 'assistant':
      return 'teal'
    case 'tool_call':
    case 'tool_result':
      return 'orange'
    case 'summary':
      return 'gray'
    default:
      return 'gray'
  }
}
