// Models: providers and their models.
//
// A connection (provider) is set up first: address, protocol and key.
// Models live inside the provider and differ only in parameters, so the
// address and key are not repeated in every entry.

import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Badge,
  Box,
  Button,
  Checkbox,
  Group,
  Modal,
  PasswordInput,
  Select,
  SimpleGrid,
  Slider,
  Stack,
  Switch,
  Text,
  TextInput,
  Title,
  Tooltip,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  deleteModel,
  deleteProvider,
  discoverModels,
  getProviders,
  saveModel,
  saveProvider,
  setDefaultModel,
  setProviderKey,
  testProvider,
  type DiscoveredModelView,
  type ModelEdit,
  type ModelView,
  type ProviderEdit,
  type ProviderView,
} from '../api/client'
import { Page, PageHeader, Panel } from '../components/layout'
import Loading from '../components/Loading'
import { t } from '../i18n'

// Protocols the daemon supports.
const protocols = [
  { value: 'openai-chat', label: 'openai-chat' },
  { value: 'openai-responses', label: 'openai-responses' },
  { value: 'anthropic', label: 'anthropic' },
]

// emptyProvider returns a blank provider for a new entry.
function emptyProvider(): ProviderEdit {
  return {
    name: '',
    protocol: 'openai-chat',
    base_url: '',
    api_key: '',
  }
}

// emptyModel returns a blank model for a new entry.
function emptyModel(provider: string): ModelEdit {
  return {
    provider,
    name: '',
    remote: '',
    // Zero means the provider did not report them: such values are not
    // pre-filled and are not sent to the provider.
    context_window: 0,
    max_tokens: 0,
    temperature: 1,
    default: false,
    reasoning: {},
    reasoning_default: '',
    supports_reasoning_effort: false,
    max_tokens_field: '',
    skip_temperature: false,
  }
}

// levelMap turns a text of levels into a map keyed by the level itself.
//
// The text is split on commas and newlines and each level is trimmed, so
// the list can be pasted in any shape.
function levelMap(text: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const part of text.split(/[,\n]/)) {
    const name = part.trim()
    if (name) {
      out[name] = name
    }
  }
  return out
}

// toProviderEdit turns a provider into an editable form.
//
// The key is deliberately empty: the server never returns secrets, so an
// empty value means keep the current key.
function toProviderEdit(p: ProviderView): ProviderEdit {
  return {
    original: p.name,
    name: p.name,
    protocol: p.protocol || 'openai-chat',
    base_url: p.base_url ?? '',
    api_key: '',
  }
}

// toModelEdit turns a model into an editable form.
function toModelEdit(m: ModelView): ModelEdit {
  const levels = m.reasoning_levels ?? []
  return {
    provider: m.provider,
    original: m.name,
    name: m.name,
    remote: m.remote ?? '',
    context_window: m.context_window,
    max_tokens: m.max_tokens,
    temperature: m.temperature,
    default: m.default,
    reasoning: m.reasoning ?? levelMap(levels.join(",")),
    reasoning_default: m.reasoning_default ?? (levels[0] ?? ''),
    supports_reasoning_effort: m.supports_reasoning_effort,
    max_tokens_field: m.max_tokens_field ?? '',
    skip_temperature: m.skip_temperature,
  }
}

// notify shows a success or failure toast.
function notify(ok: boolean, message: string) {
  notifications.show({ message, color: ok ? 'teal' : 'red', withBorder: true })
}

/** ModelsPage lists providers and manages them. */
export default function ModelsPage() {
  const queryClient = useQueryClient()
  const { data, isLoading, error } = useQuery({ queryKey: ['providers'], queryFn: getProviders })

  const [providerForm, setProviderForm] = useState<ProviderEdit | null>(null)
  const [modelForm, setModelForm] = useState<ModelEdit | null>(null)
  // The open discovery list: the provider name and its fetched models.
  const [discover, setDiscover] = useState<{ provider: string; models: DiscoveredModelView[] } | null>(
    null,
  )

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['providers'] })
  }

  const saveProviderMutation = useMutation({
    mutationFn: (edit: ProviderEdit) => saveProvider(edit),
    onSuccess: () => {
      notify(true, t('providers.saved'))
      setProviderForm(null)
      refresh()
    },
    onError: (err: unknown) => notify(false, (err as Error).message),
  })

  const removeProvider = useMutation({
    mutationFn: (name: string) => deleteProvider(name),
    onSuccess: () => {
      notify(true, t('providers.deleted'))
      refresh()
    },
    onError: (err: unknown) => notify(false, (err as Error).message),
  })

  const saveKey = useMutation({
    mutationFn: ({ name, key }: { name: string; key: string }) => setProviderKey(name, key),
    onSuccess: () => {
      notify(true, t('providers.keySaved'))
      refresh()
    },
    onError: (err: unknown) => notify(false, (err as Error).message),
  })

  const probe = useMutation({
    mutationFn: (name: string) => testProvider(name),
    onSuccess: (res) =>
      notify(res.ok, res.ok ? res.message : `${t('providers.testFailed')}: ${res.message}`),
    onError: (err: unknown) => notify(false, (err as Error).message),
  })

  const discoverMutation = useMutation({
    mutationFn: async (name: string) => ({ name, result: await discoverModels(name) }),
    onSuccess: ({ name, result }) => setDiscover({ provider: name, models: result.models }),
    onError: (err: unknown) => notify(false, (err as Error).message),
  })

  // Adds the selected models, skipping the ones already configured.
  const addDiscovered = useMutation({
    mutationFn: async (items: DiscoveredModelView[]) => {
      for (const item of items) {
        await saveModel(discoveredToEdit(discover!.provider, item))
      }
      return items.length
    },
    onSuccess: (count) => {
      notify(true, `${t('models.added')}: ${count}`)
      setDiscover(null)
      refresh()
    },
    onError: (err: unknown) => notify(false, (err as Error).message),
  })

  const saveModelMutation = useMutation({
    mutationFn: (edit: ModelEdit) => saveModel(edit),
    onSuccess: () => {
      notify(true, t('models.saved'))
      setModelForm(null)
      refresh()
    },
    onError: (err: unknown) => notify(false, (err as Error).message),
  })

  const makeDefault = useMutation({
    mutationFn: ({ provider, name }: { provider: string; name: string }) =>
      setDefaultModel(provider, name),
    onSuccess: () => {
      notify(true, t('models.defaultSet'))
      refresh()
    },
    onError: (err: unknown) => notify(false, (err as Error).message),
  })

  const removeModel = useMutation({
    mutationFn: ({ provider, name }: { provider: string; name: string }) =>
      deleteModel(provider, name),
    onSuccess: () => {
      notify(true, t('models.deleted'))
      refresh()
    },
    onError: (err: unknown) => notify(false, (err as Error).message),
  })

  if (isLoading) return <Page><Loading /></Page>
  if (error) return <Page><Loading error={error} /></Page>

  const providers = data?.providers ?? []

  return (
    <Page>
      <PageHeader title={t('models.title')}>
        <Button onClick={() => setProviderForm(emptyProvider())}>{t('nav.providers')}</Button>
      </PageHeader>

      <Text size="sm" c="dimmed" mb="md">
        {t('models.intro')}
      </Text>

      {providers.length === 0 ? (
        <Panel>
          <Stack align="center" gap="sm" py="xl">
            <Text c="dimmed">{t('providers.empty')}</Text>
            <Button onClick={() => setProviderForm(emptyProvider())}>{t('providers.add')}</Button>
          </Stack>
        </Panel>
      ) : (
        <Stack gap="md">
          {providers.map((p) => (
            <ProviderCard
              key={p.name}
              provider={p}
              onEdit={() => setProviderForm(toProviderEdit(p))}
              onDelete={() => {
                if (confirm(`${t('providers.confirmDelete')} ${p.name}?`)) {
                  removeProvider.mutate(p.name)
                }
              }}
              onTest={() => probe.mutate(p.name)}
              onSaveKey={(key) => saveKey.mutate({ name: p.name, key })}
              onAddModel={() => setModelForm(emptyModel(p.name))}
              onDiscover={() => discoverMutation.mutate(p.name)}
              discovering={discoverMutation.isPending && discoverMutation.variables === p.name}
              onMakeDefault={(m) => makeDefault.mutate({ provider: p.name, name: m.name })}
              onEditModel={(m) => setModelForm(toModelEdit(m))}
              onDeleteModel={(m) => {
                if (confirm(`${t('models.confirmDelete')} ${m.name}?`)) {
                  removeModel.mutate({ provider: p.name, name: m.name })
                }
              }}
              testing={probe.isPending}
            />
          ))}
        </Stack>
      )}

      {providerForm && (
        <ProviderModal
          form={providerForm}
          onChange={setProviderForm}
          onClose={() => setProviderForm(null)}
          onSubmit={() => saveProviderMutation.mutate(providerForm)}
          saving={saveProviderMutation.isPending}
        />
      )}

      {discover && (
        <DiscoverModal
          provider={discover.provider}
          models={discover.models}
          onClose={() => setDiscover(null)}
          onAdd={(items) => addDiscovered.mutate(items)}
          adding={addDiscovered.isPending}
        />
      )}

      {modelForm && (
        <ModelModal
          form={modelForm}
          onChange={setModelForm}
          onClose={() => setModelForm(null)}
          onSubmit={() => saveModelMutation.mutate(modelForm)}
          saving={saveModelMutation.isPending}
        />
      )}
    </Page>
  )
}

/** ProviderCard shows a provider with its key and models. */
function ProviderCard({
  provider,
  onEdit,
  onDelete,
  onTest,
  onSaveKey,
  onAddModel,
  onDiscover,
  onMakeDefault,
  onEditModel,
  onDeleteModel,
  testing,
  discovering,
}: {
  provider: ProviderView
  onEdit: () => void
  onDelete: () => void
  onTest: () => void
  onSaveKey: (key: string) => void
  onAddModel: () => void
  onDiscover: () => void
  onMakeDefault: (m: ModelView) => void
  onEditModel: (m: ModelView) => void
  onDeleteModel: (m: ModelView) => void
  testing: boolean
  discovering: boolean
}) {
  const [key, setKey] = useState('')
  const [keyOpen, setKeyOpen] = useState(false)

  return (
    <div className="provider-card">
      <Group justify="space-between" align="flex-start" wrap="nowrap" mb="md">
        <Stack gap={2} style={{ minWidth: 0 }}>
          <Group gap="xs">
            <Title order={4} m={0}>
              {provider.name}
            </Title>
            <Tooltip label={provider.has_key ? t('providers.keySet') : t('providers.keyMissing')}>
              <Badge color={provider.has_key ? 'teal' : 'orange'} variant="light">
                {provider.has_key ? t('providers.keyOk') : t('providers.keyNone')}
              </Badge>
            </Tooltip>
            <Badge color="gray" variant="outline">
              {provider.protocol || 'openai-chat'}
            </Badge>
          </Group>
          <Text size="xs" c="dimmed" className="mono">
            {provider.base_url || '-'}
          </Text>
        </Stack>

        <Group gap="xs" wrap="nowrap">
          <Button variant="light" size="xs" onClick={onTest} loading={testing}>
            {t('providers.test')}
          </Button>
          <Button variant="light" size="xs" onClick={onDiscover} loading={discovering}>
            {t('providers.fetchModels')}
          </Button>
          <Button variant="default" size="xs" onClick={() => setKeyOpen(true)}>
            {t('providers.key')}
          </Button>
          <Button variant="default" size="xs" onClick={onEdit}>
            {t('providers.edit')}
          </Button>
          <Button variant="light" color="red" size="xs" onClick={onDelete}>
            {t('providers.delete')}
          </Button>
        </Group>
      </Group>

      <Group justify="space-between" mb="xs">
        <Text size="xs" fw={600} c="dimmed" tt="uppercase">
          {t('models.count')} {provider.models.length}
        </Text>
        <Button variant="subtle" size="xs" onClick={onAddModel}>
          + {t('models.add')}
        </Button>
      </Group>

      {provider.models.length === 0 ? (
        <Text size="sm" c="dimmed">
          {t('models.empty')}
        </Text>
      ) : (
        <Stack gap="xs">
          {provider.models.map((m) => (
            <div className="model-row" key={m.name}>
              <Group justify="space-between" wrap="nowrap">
                <Stack gap={2} style={{ minWidth: 0 }}>
                  <Group gap="xs">
                    <Text size="sm" fw={600}>
                      {m.name}
                    </Text>
                    {m.default && (
                      <Badge color="brand" variant="light">
                        {t('models.defaultBadge')}
                      </Badge>
                    )}
                  </Group>
                  <Text size="xs" c="dimmed" className="mono">
                    {m.remote}
                  </Text>
                  <Group gap="md">
                    <Text size="xs" c="dimmed">
                      {t('models.context')}:{' '}
                      {m.context_window ? formatNumber(m.context_window) : t('models.notSet')}
                    </Text>
                    <Text size="xs" c="dimmed">
                      {t('models.maxTokens')}:{' '}
                      {m.max_tokens ? formatNumber(m.max_tokens) : t('models.notSet')}
                    </Text>
                    {(m.reasoning_levels?.length ?? 0) > 0 && (
                      <Text size="xs" c="dimmed">
                        {t('models.reasoning')}: {m.reasoning_levels.join(', ')}
                      </Text>
                    )}
                  </Group>
                </Stack>
                <Group gap="xs" wrap="nowrap">
                  <Button
                    variant="light"
                    size="xs"
                    onClick={() => onMakeDefault(m)}
                    disabled={m.default}
                  >
                    {t('models.setDefaultAction')}
                  </Button>
                  <Button variant="default" size="xs" onClick={() => onEditModel(m)}>
                    {t('models.edit')}
                  </Button>
                  <Button variant="light" color="red" size="xs" onClick={() => onDeleteModel(m)}>
                    {t('models.delete')}
                  </Button>
                </Group>
              </Group>
            </div>
          ))}
        </Stack>
      )}

      <Modal
        opened={keyOpen}
        onClose={() => setKeyOpen(false)}
        title={`${t('providers.key')}: ${provider.name}`}
      >
        <Stack>
          <PasswordInput
            label={t('providers.apiKey')}
            description={t('providers.apiKeyHint')}
            placeholder={provider.has_key ? t('providers.apiKeyKeep') : ''}
            value={key}
            onChange={(e) => setKey(e.currentTarget.value)}
          />
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setKeyOpen(false)}>
              {t('models.cancel')}
            </Button>
            <Button
              onClick={() => {
                onSaveKey(key)
                setKey('')
                setKeyOpen(false)
              }}
            >
              {t('models.save')}
            </Button>
          </Group>
        </Stack>
      </Modal>
    </div>
  )
}

/** ProviderModal edits a provider connection. */
function ProviderModal({
  form,
  onChange,
  onClose,
  onSubmit,
  saving,
}: {
  form: ProviderEdit
  onChange: (form: ProviderEdit) => void
  onClose: () => void
  onSubmit: () => void
  saving: boolean
}) {
  const patch = (fn: (draft: ProviderEdit) => void) => {
    const next = structuredClone(form)
    fn(next)
    onChange(next)
  }

  return (
    <Modal
      opened
      onClose={onClose}
      title={form.original ? t('providers.editTitle') : t('providers.addTitle')}
    >
      <Stack>
        <TextInput
          label={t('providers.name')}
          description={t('providers.nameHint')}
          value={form.name}
          onChange={(e) => patch((d) => { d.name = e.currentTarget.value })}
          required
        />
        <Select
          label={t('providers.protocol')}
          data={protocols}
          value={form.protocol}
          onChange={(v) => patch((d) => { d.protocol = v ?? 'openai-chat' })}
          allowDeselect={false}
        />
        <TextInput
          label={t('providers.baseUrl')}
          description={t('providers.baseUrlHint')}
          placeholder="https://api.example.com/v1"
          value={form.base_url}
          onChange={(e) => patch((d) => { d.base_url = e.currentTarget.value })}
        />
        <PasswordInput
          label={t('providers.apiKey')}
          description={form.original ? t('providers.apiKeyKeep') : t('providers.apiKeyHint')}
          value={form.api_key ?? ''}
          onChange={(e) => patch((d) => { d.api_key = e.currentTarget.value })}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t('models.cancel')}
          </Button>
          <Button onClick={onSubmit} loading={saving} disabled={!form.name}>
            {t('models.save')}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}

/** ModelModal edits a model inside a provider. */
function ModelModal({
  form,
  onChange,
  onClose,
  onSubmit,
  saving,
}: {
  form: ModelEdit
  onChange: (form: ModelEdit) => void
  onClose: () => void
  onSubmit: () => void
  saving: boolean
}) {
  const patch = (fn: (draft: ModelEdit) => void) => {
    const next = structuredClone(form)
    fn(next)
    onChange(next)
  }

  // Levels show as a comma-separated text: there are few of them and the
  // whole set stays visible at once.
  // The text is kept apart from the map: rebuilding it from the map on
  // every keystroke dropped a comma before it could be typed.
  const [levelsText, setLevelsText] = useState(() => Object.keys(form.reasoning ?? {}).join(','))

  return (
    <Modal
      opened
      onClose={onClose}
      size="lg"
      title={
        form.original
          ? `${t('models.editTitle')}: ${form.original}`
          : `${t('models.addTitle')}: ${form.provider}`
      }
    >
      <Stack>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput
            label={t('models.name')}
            description={t('models.nameHint')}
            value={form.name}
            onChange={(e) => patch((d) => { d.name = e.currentTarget.value })}
            required
          />
          <TextInput
            label={t('models.remote')}
            description={t('models.remoteHint')}
            placeholder="deepseek-ai/DeepSeek-V4-Flash"
            value={form.remote}
            onChange={(e) => patch((d) => { d.remote = e.currentTarget.value })}
          />
        </SimpleGrid>

        <SimpleGrid cols={{ base: 1, sm: 3 }}>
          <TextInput
            label={t('models.context')}
            description={t('models.contextHint')}
            placeholder={t('models.notSet')}
            value={form.context_window ? formatNumber(form.context_window) : ''}
            onChange={(e) => patch((d) => { d.context_window = parseNumber(e.currentTarget.value) })}
          />
          <TextInput
            label={t('models.maxTokens')}
            description={t('models.maxTokensHint')}
            placeholder={t('models.notSet')}
            value={form.max_tokens ? formatNumber(form.max_tokens) : ''}
            onChange={(e) => patch((d) => { d.max_tokens = parseNumber(e.currentTarget.value) })}
          />
          <Stack gap={4}>
            <Text size="sm" fw={500}>
              {t('models.temperature')}
              <Text span c="dimmed" fw={400}>
                {' '}
                {form.temperature.toFixed(2)}
              </Text>
            </Text>
            {/* Mantine draws the tick labels out of flow, so they need
                their own padding: otherwise the hint overlaps the marks. */}
            <Box pb={22}>
              <Slider
                value={form.temperature}
                onChange={(v) => patch((d) => { d.temperature = v })}
                min={0}
                max={2}
                step={0.05}
                marks={[
                  { value: 0, label: '0' },
                  { value: 1, label: '1' },
                  { value: 2, label: '2' },
                ]}
                label={(v) => v.toFixed(2)}
              />
            </Box>
            <Text size="xs" c="dimmed">
              {t('models.temperatureHint')}
            </Text>
          </Stack>
        </SimpleGrid>

        <TextInput
          label={t('models.reasoningLevels')}
          description={t('models.reasoningLevelsHint')}
          placeholder="low,medium,high,max"
          value={levelsText}
          onChange={(e) => {
            const text = e.currentTarget.value
            setLevelsText(text)
            patch((d) => { d.reasoning = levelMap(text) })
          }}
        />
        <Select
          label={t('models.reasoningDefault')}
          data={Object.keys(form.reasoning ?? {})}
          value={form.reasoning_default || null}
          onChange={(v) => patch((d) => { d.reasoning_default = v ?? '' })}
          clearable
        />

        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <Switch
            label={t('models.setDefault')}
            description={t('models.setDefaultHint')}
            checked={form.default}
            onChange={(e) => patch((d) => { d.default = e.currentTarget.checked })}
          />
          <Switch
            label={t('models.reasoningEffort')}
            description={t('models.reasoningEffortHint')}
            checked={form.supports_reasoning_effort}
            onChange={(e) => patch((d) => { d.supports_reasoning_effort = e.currentTarget.checked })}
          />
          <Switch
            label={t('models.noTemperature')}
            description={t('models.noTemperatureHint')}
            checked={form.skip_temperature}
            onChange={(e) => patch((d) => { d.skip_temperature = e.currentTarget.checked })}
          />
        </SimpleGrid>

        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t('models.cancel')}
          </Button>
          <Button onClick={onSubmit} loading={saving} disabled={!form.name}>
            {t('models.save')}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}

// formatNumber groups digits with spaces so large numbers stay readable.
function formatNumber(value: number): string {
  if (!value) return '0'
  return value.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ' ')
}

// parseNumber reads a number from a field, ignoring digit separators.
function parseNumber(text: string): number {
  const digits = text.replace(/[^\d]/g, '')
  return digits ? Number(digits) : 0
}

/** DiscoverModal lists the provider models and adds the selected ones. */
function DiscoverModal({
  provider,
  models,
  onClose,
  onAdd,
  adding,
}: {
  provider: string
  models: DiscoveredModelView[]
  onClose: () => void
  onAdd: (items: DiscoveredModelView[]) => void
  adding: boolean
}) {
  // Only unconfigured models are pre-checked: existing ones are not added
  // again, so their checkbox would do nothing.
  const [checked, setChecked] = useState<Set<string>>(
    () => new Set(models.filter((m) => !m.configured).map((m) => m.id)),
  )

  const available = models.filter((m) => !m.configured)
  const allChecked = available.length > 0 && available.every((m) => checked.has(m.id))

  const toggle = (id: string) => {
    const next = new Set(checked)
    if (next.has(id)) {
      next.delete(id)
    } else {
      next.add(id)
    }
    setChecked(next)
  }

  const toggleAll = () => {
    setChecked(allChecked ? new Set() : new Set(available.map((m) => m.id)))
  }

  return (
    <Modal opened onClose={onClose} size="lg" title={`${t('providers.fetchModels')}: ${provider}`}>
      <Stack>
        {models.length === 0 ? (
          <Text c="dimmed">{t('models.discoverEmpty')}</Text>
        ) : (
          <>
            <Group justify="space-between">
              <Checkbox
                label={t('models.selectAll')}
                checked={allChecked}
                indeterminate={checked.size > 0 && !allChecked}
                onChange={toggleAll}
              />
              <Text size="xs" c="dimmed">
                {t('models.discoverCount')}: {checked.size} / {available.length}
              </Text>
            </Group>

            <div style={{ maxHeight: 420, overflow: 'auto' }}>
              <Stack gap={4}>
                {models.map((m) => (
                  <Checkbox.Card
                    key={m.id}
                    checked={m.configured || checked.has(m.id)}
                    onClick={() => {
                      if (!m.configured) {
                        toggle(m.id)
                      }
                    }}
                    style={{ padding: '8px 10px' }}
                  >
                    <Group justify="space-between" wrap="nowrap">
                      <Group gap="xs" style={{ minWidth: 0 }}>
                        <Checkbox.Indicator
                          checked={m.configured || checked.has(m.id)}
                          disabled={m.configured}
                        />
                        <Stack gap={0} style={{ minWidth: 0 }}>
                          <Text size="sm" className="mono" style={{ wordBreak: 'break-all' }}>
                            {m.id}
                          </Text>
                          <Text size="xs" c="dimmed">
                            {m.context_window > 0
                              ? `${t('models.context')}: ${formatNumber(m.context_window)}`
                              : t('models.contextUnknown')}
                            {' · '}
                            {m.max_tokens > 0
                              ? `${t('models.maxTokens')}: ${formatNumber(m.max_tokens)}`
                              : t('models.maxTokensUnknown')}
                          </Text>
                        </Stack>
                      </Group>
                      {m.configured && (
                        <Badge color="teal" variant="light">
                          {t('models.alreadyAdded')}
                        </Badge>
                      )}
                    </Group>
                  </Checkbox.Card>
                ))}
              </Stack>
            </div>
          </>
        )}

        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t('models.cancel')}
          </Button>
          <Button
            onClick={() => onAdd(models.filter((m) => checked.has(m.id)))}
            loading={adding}
            disabled={checked.size === 0}
          >
            {t('models.addSelected')}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}

// discoveredToEdit turns a discovered model into a model entry.
//
// Context window and output limit come from the provider response; when
// it reports nothing they stay at the defaults and can be edited later.
function discoveredToEdit(provider: string, item: DiscoveredModelView): ModelEdit {
  const edit = emptyModel(provider)
  edit.name = localModelName(provider, item.id)
  edit.remote = item.id
  if (item.context_window > 0) {
    edit.context_window = item.context_window
  }
  if (item.max_tokens > 0) {
    edit.max_tokens = item.max_tokens
  }
  return edit
}

// localModelName builds an entry name from the provider model id.
//
// The name must pass the daemon check: letters, digits, dot, dash and
// underscore, with no leading separator. Other characters become a dash,
// and a leading digit gets the provider prefix.
function localModelName(provider: string, remote: string): string {
  let name = remote.replace(/[^a-zA-Z0-9._-]+/g, '-').replace(/^-+|-+$/g, '')
  if (!name) {
    // The id has no usable characters: fall back to the provider name.
    return `${provider}-model`
  }
  if (!/^[a-zA-Z]/.test(name)) {
    name = `${provider}-${name}`
  }
  return name
}
