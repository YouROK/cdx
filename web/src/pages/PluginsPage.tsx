// Plugins: install by address, enable, remove and inspect.

import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Anchor,
  Badge,
  Button,
  Group,
  Modal,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
  Tooltip,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { deletePlugin, getPlugins, installPlugin, setPlugin, type PluginView } from '../api/client'
import { Page, PageHeader, Panel } from '../components/layout'
import Loading from '../components/Loading'
import { t } from '../i18n'

// notify shows a success or failure toast.
function notify(ok: boolean, message: string) {
  notifications.show({ message, color: ok ? 'teal' : 'red', withBorder: true })
}

/** PluginsPage lists plugins and manages them. */
export default function PluginsPage() {
  const queryClient = useQueryClient()
  const { data, isLoading, error } = useQuery({ queryKey: ['plugins'], queryFn: getPlugins })
  const [url, setUrl] = useState('')
  const [details, setDetails] = useState<PluginView | null>(null)

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['plugins'] })
  }

  const install = useMutation({
    mutationFn: (target: string) => installPlugin(target),
    onSuccess: (res) => {
      notify(true, `${t('plugins.installed')}: ${res.plugin.name} ${res.plugin.version}`)
      setUrl('')
      refresh()
    },
    onError: (err: unknown) => notify(false, (err as Error).message),
  })

  const toggle = useMutation({
    mutationFn: ({ name, enabled }: { name: string; enabled: boolean }) =>
      setPlugin(name, enabled),
    onSuccess: refresh,
    onError: (err: unknown) => notify(false, (err as Error).message),
  })

  const remove = useMutation({
    mutationFn: (name: string) => deletePlugin(name),
    onSuccess: refresh,
    onError: (err: unknown) => notify(false, (err as Error).message),
  })

  if (isLoading) return <Page><Loading /></Page>
  if (error) return <Page><Loading error={error} /></Page>

  const plugins = data?.plugins ?? []

  return (
    <Page>
      <PageHeader title={t('plugins.title')} />

      <Panel mb="md">
        <Title order={4} mb="md">
          {t('plugins.installTitle')}
        </Title>
        {/* The hint sits outside the field: inside it falls below the
            input and stretches the wrapper, which pushes the button down. */}
        <Group align="flex-end" wrap="nowrap">
          <TextInput
            label={t('plugins.installUrl')}
            placeholder="https://raw.githubusercontent.com/owner/repo/v1.0.0/manifest.json"
            value={url}
            onChange={(e) => setUrl(e.currentTarget.value)}
            style={{ flex: 1 }}
          />
          <Button
            onClick={() => install.mutate(url)}
            loading={install.isPending}
            disabled={!url}
          >
            {t('plugins.install')}
          </Button>
        </Group>
        <Text size="xs" c="dimmed" mt={4}>
          {t('plugins.installUrlHint')}
        </Text>
      </Panel>

      <Panel p={0}>
        {plugins.length === 0 ? (
          <Text c="dimmed" p="lg">
            {t('plugins.empty')}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={0}>
            {/* Fixed layout: long values would otherwise widen the table
                and the row would not fit the screen. */}
            <Table
              highlightOnHover
              verticalSpacing="sm"
              horizontalSpacing="md"
              style={{ tableLayout: 'fixed', width: '100%' }}
            >
              <Table.Thead>
                <Table.Tr>
                  <Table.Th style={{ width: '19%' }}>{t('plugins.name')}</Table.Th>
                  <Table.Th style={{ width: '9%' }}>{t('plugins.version')}</Table.Th>
                  <Table.Th style={{ width: '24%' }}>{t('plugins.description')}</Table.Th>
                  <Table.Th style={{ width: '15%' }}>{t('plugins.tools')}</Table.Th>
                  <Table.Th style={{ width: '11%' }}>{t('plugins.status')}</Table.Th>
                  <Table.Th style={{ width: '22%' }} />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {plugins.map((p) => (
                  <Table.Tr key={p.name}>
                    <Table.Td>
                      <Text
                        size="sm"
                        fw={600}
                        style={{
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
                        }}
                      >
                        {p.name}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm" c="dimmed">
                        {p.version || '-'}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Cell value={p.description} />
                    </Table.Td>
                    <Table.Td>
                      <Cell value={(p.tools ?? []).join(', ')} mono />
                    </Table.Td>
                    <Table.Td>
                      <Badge
                        color={p.loaded ? 'teal' : 'gray'}
                        variant="light"
                        style={{ whiteSpace: 'nowrap', flexShrink: 0 }}
                      >
                        {p.loaded ? t('plugins.enabled') : t('plugins.disabled')}
                      </Badge>
                    </Table.Td>
                    <Table.Td>
                      <Group gap={6} justify="flex-end" wrap="nowrap">
                        <Button variant="light" size="xs" px={8} onClick={() => setDetails(p)}>
                          {t('plugins.info')}
                        </Button>
                        <Button
                          variant="light"
                          size="xs"
                          px={8}
                          onClick={() => toggle.mutate({ name: p.name, enabled: !p.loaded })}
                        >
                          {p.loaded ? t('plugins.disable') : t('plugins.enable')}
                        </Button>
                        <Button
                          variant="light"
                          color="red"
                          size="xs"
                          px={8}
                          onClick={() => {
                            if (confirm(`${t('plugins.confirmDelete')} ${p.name}?`)) {
                              remove.mutate(p.name)
                            }
                          }}
                        >
                          {t('plugins.delete')}
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

      {details && <PluginDetails plugin={details} onClose={() => setDetails(null)} />}
    </Page>
  )
}

/** Cell shows a value on one line, with the full text in a tooltip. */
function Cell({ value, mono }: { value: string; mono?: boolean }) {
  const text = value || '-'
  return (
    <Tooltip label={text} disabled={text === '-'} multiline w={420} openDelay={200}>
      {/* The ellipsis is applied by hand: Mantine's truncate leaves
          nowrap in place, which stops the cell from shrinking. */}
      <Text
        size="xs"
        c="dimmed"
        className={mono ? 'mono' : undefined}
        style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
      >
        {text}
      </Text>
    </Tooltip>
  )
}

/**
 * permissionColor picks the color of a permission badge.
 *
 * Dangerous is red and edit is orange, so the risk is visible before
 * installing. A light background keeps it from reading as an error.
 */
function permissionColor(level: string): string {
  switch (level) {
    case 'dangerous':
      return 'red'
    case 'edit':
      return 'orange'
    default:
      return 'gray'
  }
}

/** PermissionBadge shows a permission level colored by risk. */
function PermissionBadge({ level, size }: { level: string; size?: string }) {
  return (
    <Badge color={permissionColor(level)} variant="light" size={size}>
      {level}
    </Badge>
  )
}

/** PluginDetails shows the full manifest details. */
function PluginDetails({ plugin, onClose }: { plugin: PluginView; onClose: () => void }) {
  const rows: { label: string; value: string }[] = [
    { label: t('plugins.name'), value: plugin.name },
    { label: t('plugins.version'), value: plugin.version || '-' },
    { label: t('plugins.author'), value: plugin.author || '-' },
    { label: t('plugins.minVersion'), value: plugin.min_cdx_version || '-' },
  ]

  return (
    <Modal opened onClose={onClose} size="lg" title={`${t('plugins.info')}: ${plugin.name}`}>
      <Stack gap="md">
        <Text size="sm">{plugin.description || '-'}</Text>

        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          {rows.map((row) => (
            <Stack key={row.label} gap={2}>
              <Text size="xs" c="dimmed" tt="uppercase" fw={600}>
                {row.label}
              </Text>
              <Text size="sm">{row.value}</Text>
            </Stack>
          ))}
          <Stack gap={2}>
            <Text size="xs" c="dimmed" tt="uppercase" fw={600}>
              {t('plugins.permission')}
            </Text>
            {plugin.permission ? (
              <Group gap="xs">
                <PermissionBadge level={plugin.permission} />
                {plugin.permission !== 'read' && (
                  <Text size="xs" c="dimmed">
                    {t('plugins.permissionNote')}
                  </Text>
                )}
              </Group>
            ) : (
              <Text size="sm">-</Text>
            )}
          </Stack>
        </SimpleGrid>

        {plugin.homepage && (
          <Stack gap={2}>
            <Text size="xs" c="dimmed" tt="uppercase" fw={600}>
              {t('plugins.homepage')}
            </Text>
            <Anchor href={plugin.homepage} target="_blank" rel="noreferrer" size="sm">
              {plugin.homepage}
            </Anchor>
          </Stack>
        )}

        {plugin.capabilities?.length > 0 && (
          <Stack gap={2}>
            <Text size="xs" c="dimmed" tt="uppercase" fw={600}>
              {t('plugins.capabilities')}
            </Text>
            <Text size="sm" className="mono">
              {plugin.capabilities.join(', ')}
            </Text>
          </Stack>
        )}

        <Stack gap="xs">
          <Text size="xs" c="dimmed" tt="uppercase" fw={600}>
            {t('plugins.tools')}: {plugin.tool_details?.length ?? 0}
          </Text>
          {(plugin.tool_details ?? []).map((tool) => (
            <div key={tool.name} className="model-row">
              <Group gap="xs" align="flex-start" wrap="nowrap">
                <Text size="sm" className="mono" fw={600} style={{ minWidth: 160 }}>
                  {tool.name}
                </Text>
                {tool.permission && <PermissionBadge level={tool.permission} size="xs" />}
              </Group>
              <Text size="xs" c="dimmed" mt={2}>
                {tool.description || '-'}
              </Text>
            </div>
          ))}
        </Stack>

        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t('plugins.close')}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
