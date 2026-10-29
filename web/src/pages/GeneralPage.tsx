// General program settings: state, agent options, permissions and prompt.
//
// They live in one section because they apply to the whole program rather
// than to a single model or connection.

import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Badge,
  Button,
  Divider,
  Grid,
  Group,
  NumberInput,
  Select,
  SimpleGrid,
  Switch,
  Stack,
  Text,
  Textarea,
  TextInput,
  Title,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  getConfig,
  getPermissions,
  getRules,
  getStatus,
  putConfig,
  savePermissions,
  saveRules,
  type ConfigView,
  type DangerRuleView,
  type PermissionsView,
} from '../api/client'
import { HintIcon } from '../components/hint'
import { Page, PageHeader, Panel, SectionTitle } from '../components/layout'
import Loading from '../components/Loading'
import { t } from '../i18n'

// rulesToText turns rules into editable text.
function rulesToText(rules: DangerRuleView[]): string {
  return rules.map((r) => `${r.name} | ${r.severity} | ${r.pattern}`).join('\n')
}

// textToRules parses the text back into rules.
//
// Line format: name | severity | regular expression. Blank lines are
// skipped, bad ones come back as a parse error.
export function textToRules(text: string): { rules: DangerRuleView[]; error: string } {
  const rules: DangerRuleView[] = []
  const lines = text.split('\n')

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i].trim()
    if (!line) {
      continue
    }
    const parts = line.split('|').map((p) => p.trim())
    if (parts.length !== 3) {
      return { rules: [], error: `${t('rules.line')} ${i + 1}: ${t('rules.format')}` }
    }
    const [name, severity, pattern] = parts
    if (!name || !pattern) {
      return { rules: [], error: `${t('rules.line')} ${i + 1}: ${t('rules.required')}` }
    }
    rules.push({ name, severity: severity || 'high', pattern })
  }
  return { rules, error: '' }
}

// notify shows a success or failure toast.
function notify(ok: boolean, message: string) {
  notifications.show({
    message,
    color: ok ? 'teal' : 'red',
    withBorder: true,
  })
}

/** GeneralPage gathers the general program settings. */
export default function GeneralPage() {
  const queryClient = useQueryClient()

  const status = useQuery({ queryKey: ['status'], queryFn: getStatus })
  const config = useQuery({ queryKey: ['config'], queryFn: getConfig })
  const perms = useQuery({ queryKey: ['permissions'], queryFn: getPermissions })
  const rules = useQuery({ queryKey: ['rules'], queryFn: getRules })

  const [cfg, setCfg] = useState<ConfigView | null>(null)
  const [permForm, setPermForm] = useState<PermissionsView | null>(null)
  const [rulesText, setRulesText] = useState('')
  const [parseError, setParseError] = useState('')

  // Forms are filled once so a refetch does not wipe unsaved edits.
  useEffect(() => {
    if (config.data && !cfg) {
      setCfg(structuredClone(config.data))
    }
  }, [config.data, cfg])

  useEffect(() => {
    if (perms.data && !permForm) {
      setPermForm(structuredClone(perms.data))
    }
  }, [perms.data, permForm])

  useEffect(() => {
    if (rules.data) {
      setRulesText(rulesToText(rules.data.rules))
    }
  }, [rules.data])


  const saveCfg = useMutation({
    mutationFn: (view: ConfigView) => putConfig(view),
    onSuccess: (res) => {
      notify(true, res.restart_required ? t('settings.restart') : t('settings.saved'))
      void queryClient.invalidateQueries({ queryKey: ['config'] })
    },
    onError: (error: unknown) => notify(false, (error as Error).message),
  })

  const savePerms = useMutation({
    mutationFn: (view: PermissionsView) => savePermissions(view),
    onSuccess: () => {
      notify(true, t('permissions.saved'))
      void queryClient.invalidateQueries({ queryKey: ['permissions'] })
    },
    onError: (error: unknown) => notify(false, (error as Error).message),
  })

  const saveRulesMutation = useMutation({
    mutationFn: (list: DangerRuleView[]) => saveRules(list),
    onSuccess: () => {
      notify(true, t('rules.saved'))
      void queryClient.invalidateQueries({ queryKey: ['rules'] })
    },
    onError: (error: unknown) => notify(false, (error as Error).message),
  })

  if (status.isLoading || config.isLoading) return <Page><Loading /></Page>
  if (status.error) return <Page><Loading error={status.error} /></Page>
  if (config.error) return <Page><Loading error={config.error} /></Page>
  if (!cfg || !status.data) return null

  const patch = (fn: (draft: ConfigView) => void) => {
    const next = structuredClone(cfg)
    fn(next)
    setCfg(next)
  }

  const levels = [
    { value: 'allow', label: t('level.allow') },
    { value: 'ask', label: t('level.ask') },
    { value: 'deny', label: t('level.deny') },
  ]

  return (
    <Page>
      <PageHeader title={t('general.title')} />

      <Stack gap="md">
        <StatePanel />

        <Panel>
          <SectionTitle>{t('general.agent')}</SectionTitle>
          <Stack gap="md">
            <SimpleGrid cols={{ base: 1, sm: 2 }}>
              <NumberInput
                label={t('settings.iterations')}
                description={t('settings.iterationsHint')}
                value={cfg.agent.max_iterations}
                min={1}
                onChange={(v) => patch((d) => { d.agent.max_iterations = Number(v) || 0 })}
              />
              <NumberInput
                label={t('settings.timeout')}
                description={t('settings.timeoutHint')}
                value={cfg.agent.turn_timeout_ms}
                min={1000}
                step={1000}
                onChange={(v) => patch((d) => { d.agent.turn_timeout_ms = Number(v) || 0 })}
              />
              <TextInput
                label={t('settings.model')}
                description={t('settings.modelHint')}
                placeholder={t('settings.modelPlaceholder')}
                value={cfg.agent.model}
                onChange={(e) => patch((d) => { d.agent.model = e.currentTarget.value })}
              />
            </SimpleGrid>

            <Divider label={t('settings.context')} labelPosition="left" />

            <SimpleGrid cols={{ base: 1, sm: 2 }}>
              <Select
                label={t('settings.strategy')}
                description={t('settings.strategyHint')}
                data={[
                  { value: 'hybrid', label: t('strategy.hybrid') },
                  { value: 'recent', label: t('strategy.recent') },
                ]}
                value={cfg.context.strategy}
                onChange={(v) => patch((d) => { d.context.strategy = v ?? 'hybrid' })}
                allowDeselect={false}
              />
              <NumberInput
                label={t('settings.recent')}
                value={cfg.context.recent_messages}
                min={1}
                onChange={(v) => patch((d) => { d.context.recent_messages = Number(v) || 0 })}
              />
            </SimpleGrid>

            <Group>
              <Button onClick={() => saveCfg.mutate(cfg)} loading={saveCfg.isPending}>
                {t('settings.save')}
              </Button>
            </Group>
          </Stack>
        </Panel>

        <Panel>
          <Group gap="xs" mb="md">
            <SectionTitle>{t('general.output')}</SectionTitle>
          </Group>
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            <Select
              label={t('settings.language')}
              description={t('settings.languageHint')}
              data={[
                { value: 'auto', label: t('lang.auto') },
                { value: 'en', label: 'English' },
                { value: 'ru', label: 'Русский' },
              ]}
              value={cfg.output.language}
              onChange={(v) => patch((d) => { d.output.language = v ?? 'auto' })}
              allowDeselect={false}
            />
            <Select
              label={t('settings.color')}
              data={[
                { value: 'auto', label: t('lang.auto') },
                { value: 'always', label: t('color.always') },
                { value: 'never', label: t('color.never') },
              ]}
              value={cfg.output.color}
              onChange={(v) => patch((d) => { d.output.color = v ?? 'auto' })}
              allowDeselect={false}
            />
          </SimpleGrid>

          <Switch
            mt="md"
            label={t('settings.bell')}
            description={t('settings.bellHint')}
            checked={cfg.output.bell ?? false}
            onChange={(e) => patch((d) => { d.output.bell = e.currentTarget.checked })}
          />

          <Group mt="md">
            <Button onClick={() => saveCfg.mutate(cfg)} loading={saveCfg.isPending}>
              {t('settings.save')}
            </Button>
          </Group>
        </Panel>

        <Panel>
          <SectionTitle>{t('permissions.title')}</SectionTitle>
          <Text size="sm" c="dimmed" mb="md">
            {t('permissions.hint')}
          </Text>
          {permForm && (
            <>
              <SimpleGrid cols={{ base: 1, sm: 2 }}>
                <Select
                  label={t('permissions.read')}
                  description={t('permissions.readHint')}
                  data={levels}
                  value={permForm.read}
                  onChange={(v) => setPermForm({ ...permForm, read: v ?? 'allow' })}
                  allowDeselect={false}
                />
                <Select
                  label={t('permissions.edit')}
                  description={t('permissions.editHint')}
                  data={levels}
                  value={permForm.edit}
                  onChange={(v) => setPermForm({ ...permForm, edit: v ?? 'ask' })}
                  allowDeselect={false}
                />
                <Select
                  label={t('permissions.dangerous')}
                  description={t('permissions.dangerousHint')}
                  data={levels}
                  value={permForm.dangerous}
                  onChange={(v) => setPermForm({ ...permForm, dangerous: v ?? 'ask' })}
                  allowDeselect={false}
                />
                <Select
                  label={t('permissions.nonInteractive')}
                  description={t('permissions.nonInteractiveHint')}
                  data={[
                    { value: 'allow', label: t('level.allow') },
                    { value: 'deny', label: t('level.deny') },
                  ]}
                  value={permForm.non_interactive}
                  onChange={(v) => setPermForm({ ...permForm, non_interactive: v ?? 'deny' })}
                  allowDeselect={false}
                />
              </SimpleGrid>
              <Group mt="md">
                <Button onClick={() => savePerms.mutate(permForm)} loading={savePerms.isPending}>
                  {t('permissions.save')}
                </Button>
              </Group>
            </>
          )}
        </Panel>

        <Panel>
          <Group gap="xs" mb="md">
            <SectionTitle>{t('rules.title')}</SectionTitle>
            <HintIcon label={t('rules.hint')} />
          </Group>
          <Textarea
            value={rulesText}
            onChange={(e) => {
              setRulesText(e.currentTarget.value)
              setParseError('')
            }}
            autosize
            minRows={6}
            styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)', fontSize: 12 } }}
          />
          <Group mt="md">
            <Button
              onClick={() => {
                const parsed = textToRules(rulesText)
                if (parsed.error) {
                  setParseError(parsed.error)
                  return
                }
                saveRulesMutation.mutate(parsed.rules)
              }}
              loading={saveRulesMutation.isPending}
            >
              {t('rules.save')}
            </Button>
            {parseError && <Text c="red" size="sm">{parseError}</Text>}
          </Group>
        </Panel>

      </Stack>
    </Page>
  )
}

/** StatePanel shows the daemon state and its version. */
function StatePanel() {
  const { data } = useQuery({ queryKey: ['status'], queryFn: getStatus })
  if (!data) return null

  const ok = data.state === 'running'
  const rows: { label: string; value: string }[] = [
    { label: t('status.version'), value: data.version },
    { label: t('status.protocol'), value: String(data.proto) },
    { label: t('status.pid'), value: String(data.pid) },
    { label: t('status.uptime'), value: formatUptime(data.uptime_s) },
  ]

  return (
    <Panel>
      <Group justify="space-between" mb="md" wrap="nowrap">
        <Group gap="xs">
          <Title order={4} m={0}>
            {t('status.title')}
          </Title>
          <Badge color={ok ? 'teal' : 'orange'}>{data.state}</Badge>
        </Group>
      </Group>
      <Grid>
        {rows.map((row) => (
          <Grid.Col key={row.label} span={{ base: 6, sm: 3 }}>
            <Text size="xs" c="dimmed" tt="uppercase" fw={600}>
              {row.label}
            </Text>
            <Text size="sm" className="mono">
              {row.value}
            </Text>
          </Grid.Col>
        ))}
      </Grid>
    </Panel>
  )
}

// formatUptime turns seconds into a short label.
function formatUptime(seconds: number): string {
  if (seconds < 60) return `${seconds} ${t('common.seconds')}`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} ${t('common.minutes')}`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} ${t('common.hours')}`
  const days = Math.floor(hours / 24)
  return `${days} ${t('common.days')}`
}
