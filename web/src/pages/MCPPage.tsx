// MCP servers: connections, their state and restart.

import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Badge,
  Button,
  Group,
  Modal,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Table,
  Text,
  Textarea,
  TextInput,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  deleteMCP,
  getMCP,
  restartMCP,
  saveMCP,
  type MCPEdit,
  type MCPView,
} from '../api/client'
import { Page, PageHeader, Panel } from '../components/layout'
import Loading from '../components/Loading'
import { t } from '../i18n'

// emptyMCP returns a blank server for a new entry.
function emptyMCP(): MCPEdit {
  return {
    name: '',
    transport: 'stdio',
    command: '',
    args: [],
    url: '',
    enabled: true,
    timeout_ms: 30000,
    permission: 'edit',
    env: {},
    cwd: '',
    auto_restart: true,
  }
}

// toEdit turns a server into an editable form.
function toEdit(s: MCPView): MCPEdit {
  return {
    original: s.name,
    name: s.name,
    transport: s.transport,
    command: s.command,
    args: s.args ?? [],
    url: s.url,
    enabled: s.enabled,
    timeout_ms: s.timeout_ms,
    permission: s.permission,
    env: s.env ?? {},
    cwd: s.cwd,
    auto_restart: s.auto_restart,
  }
}

// notify shows a success or failure toast.
function notify(ok: boolean, message: string) {
  notifications.show({ message, color: ok ? 'teal' : 'red', withBorder: true })
}

/** MCPPage lists and edits MCP servers. */
export default function MCPPage() {
  const queryClient = useQueryClient()
  // Connecting happens in the background, so the list polls on its own:
  // otherwise the start result stays invisible until a page reload.
  const { data, isLoading, error } = useQuery({
    queryKey: ['mcp'],
    queryFn: getMCP,
    refetchInterval: 3000,
  })
  const [form, setForm] = useState<MCPEdit | null>(null)

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['mcp'] })
  }

  const save = useMutation({
    mutationFn: (edit: MCPEdit) => saveMCP(edit),
    onSuccess: () => {
      notify(true, t('mcp.saved'))
      setForm(null)
      refresh()
    },
    onError: (err: unknown) => notify(false, (err as Error).message),
  })

  const remove = useMutation({
    mutationFn: (name: string) => deleteMCP(name),
    onSuccess: () => {
      notify(true, t('mcp.deleted'))
      refresh()
    },
    onError: (err: unknown) => notify(false, (err as Error).message),
  })

  const restart = useMutation({
    mutationFn: (name: string) => restartMCP(name),
    onSuccess: () => {
      notify(true, t('mcp.restarted'))
      refresh()
    },
    onError: (err: unknown) => notify(false, (err as Error).message),
  })

  if (isLoading) return <Page><Loading /></Page>
  if (error) return <Page><Loading error={error} /></Page>

  const servers = data?.servers ?? []

  return (
    <Page>
      <PageHeader title={t('mcp.title')}>
        <Button onClick={() => setForm(emptyMCP())}>{t('mcp.add')}</Button>
      </PageHeader>

      <Text size="sm" c="dimmed" mb="md">
        {t('mcp.intro')}
      </Text>

      <Panel p={0}>
        {servers.length === 0 ? (
          <Text c="dimmed" p="lg">
            {t('mcp.empty')}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={720}>
            <Table highlightOnHover verticalSpacing="sm" horizontalSpacing="md">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t('mcp.name')}</Table.Th>
                  <Table.Th>{t('mcp.transport')}</Table.Th>
                  <Table.Th>{t('mcp.status')}</Table.Th>
                  <Table.Th>{t('mcp.tools')}</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {servers.map((s) => (
                  <Table.Tr key={s.name}>
                    <Table.Td>
                      <Stack gap={2}>
                        <Text size="sm" fw={600}>
                          {s.name}
                        </Text>
                        <Text size="xs" c="dimmed" className="mono">
                          {s.transport === 'http' ? s.url : `${s.command} ${(s.args ?? []).join(' ')}`}
                        </Text>
                      </Stack>
                    </Table.Td>
                    <Table.Td>
                      <Badge color="gray" variant="outline">
                        {s.transport || 'stdio'}
                      </Badge>
                    </Table.Td>
                    <Table.Td>
                      <ServerStatus server={s} />
                    </Table.Td>
                    <Table.Td>{s.tools || 0}</Table.Td>
                    <Table.Td>
                      <Group gap="xs" justify="flex-end" wrap="nowrap">
                        <Button
                          variant="default"
                          size="xs"
                          onClick={() => restart.mutate(s.name)}
                          loading={restart.isPending}
                        >
                          {t('mcp.restart')}
                        </Button>
                        <Button variant="default" size="xs" onClick={() => setForm(toEdit(s))}>
                          {t('mcp.edit')}
                        </Button>
                        <Button
                          variant="light"
                          color="red"
                          size="xs"
                          onClick={() => {
                            if (confirm(`${t('mcp.confirmDelete')} ${s.name}?`)) {
                              remove.mutate(s.name)
                            }
                          }}
                        >
                          {t('mcp.delete')}
                        </Button>
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Panel>

      {form && (
        <MCPModal
          form={form}
          onChange={setForm}
          onClose={() => setForm(null)}
          onSubmit={(edit) => save.mutate(edit)}
          saving={save.isPending}
        />
      )}
    </Page>
  )
}

/** ServerStatus shows the connection state of a server. */
function ServerStatus({ server }: { server: MCPView }) {
  if (!server.enabled) {
    return <Badge color="gray">{t('mcp.off')}</Badge>
  }
  if (server.failed) {
    return <Badge color="red">{t('mcp.failed')}</Badge>
  }
  if (server.connected) {
    return <Badge color="teal">{t('mcp.connected')}</Badge>
  }
  return <Badge color="orange">{t('mcp.pending')}</Badge>
}

/** MCPModal edits a server. */
function MCPModal({
  form,
  onChange,
  onClose,
  onSubmit,
  saving,
}: {
  form: MCPEdit
  onChange: (form: MCPEdit) => void
  onClose: () => void
  onSubmit: (form: MCPEdit) => void
  saving: boolean
}) {
  // Arguments and environment are edited as plain text: these fields are
  // rare, and a dedicated editor is not worth the code.
  const [argsText, setArgsText] = useState((form.args ?? []).join(' '))
  const [envText, setEnvText] = useState(
    Object.entries(form.env ?? {})
      .map(([k, v]) => `${k}=${v}`)
      .join('\n'),
  )

  const patch = (fn: (draft: MCPEdit) => void) => {
    const next = structuredClone(form)
    fn(next)
    onChange(next)
  }

  // submit folds the text fields into the form and sends the result.
  //
  // It cannot go through onChange: state updates are async, so the send
  // would carry the previous form without the arguments.
  const submit = () => {
    const next = structuredClone(form)
    next.args = argsText.split(/\s+/).filter(Boolean)
    next.env = parseEnv(envText)
    onSubmit(next)
  }

  return (
    <Modal
      opened
      onClose={onClose}
      size="lg"
      title={form.original ? t('mcp.editTitle') : t('mcp.addTitle')}
    >
      <Stack>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput
            label={t('mcp.name')}
            value={form.name}
            onChange={(e) => patch((d) => { d.name = e.currentTarget.value })}
            required
          />
          <Select
            label={t('mcp.transport')}
            data={[
              { value: 'stdio', label: 'stdio' },
              { value: 'http', label: 'http' },
            ]}
            value={form.transport}
            onChange={(v) => patch((d) => { d.transport = v ?? 'stdio' })}
            allowDeselect={false}
          />
        </SimpleGrid>

        {form.transport === 'http' ? (
          <TextInput
            label={t('mcp.url')}
            placeholder="https://example.com/mcp"
            value={form.url}
            onChange={(e) => patch((d) => { d.url = e.currentTarget.value })}
          />
        ) : (
          <>
            <TextInput
              label={t('mcp.command')}
              placeholder="npx"
              value={form.command}
              onChange={(e) => patch((d) => { d.command = e.currentTarget.value })}
            />
            <TextInput
              label={t('mcp.args')}
              description={t('mcp.argsHint')}
              value={argsText}
              onChange={(e) => setArgsText(e.currentTarget.value)}
            />
          </>
        )}

        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput
            label={t('mcp.timeout')}
            value={String(form.timeout_ms)}
            onChange={(e) => patch((d) => { d.timeout_ms = Number(e.currentTarget.value) || 0 })}
          />
          <Select
            label={t('mcp.permission')}
            data={[
              { value: 'read', label: t('permissions.read') },
              { value: 'edit', label: t('permissions.edit') },
              { value: 'dangerous', label: t('permissions.dangerous') },
            ]}
            value={form.permission}
            onChange={(v) => patch((d) => { d.permission = v ?? 'edit' })}
            allowDeselect={false}
          />
        </SimpleGrid>

        <Textarea
          label={t('mcp.env')}
          description={t('mcp.envHint')}
          value={envText}
          onChange={(e) => setEnvText(e.currentTarget.value)}
          autosize
          minRows={3}
          styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)', fontSize: 12 } }}
        />

        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <Switch
            label={t('mcp.enabled')}
            checked={form.enabled}
            onChange={(e) => patch((d) => { d.enabled = e.currentTarget.checked })}
          />
          <Switch
            label={t('mcp.autoRestart')}
            checked={form.auto_restart}
            onChange={(e) => patch((d) => { d.auto_restart = e.currentTarget.checked })}
          />
        </SimpleGrid>

        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t('mcp.cancel')}
          </Button>
          <Button onClick={submit} loading={saving} disabled={!form.name}>
            {t('mcp.save')}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}

// parseEnv parses KEY=value lines.
function parseEnv(text: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of text.split('\n')) {
    const trimmed = line.trim()
    if (!trimmed) continue
    const idx = trimmed.indexOf('=')
    if (idx <= 0) continue
    out[trimmed.slice(0, idx).trim()] = trimmed.slice(idx + 1).trim()
  }
  return out
}
