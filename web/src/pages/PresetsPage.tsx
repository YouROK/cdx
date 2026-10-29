// Agent presets: working modes and their prompts.
//
// A preset only changes the explanatory part of the prompt: what the model
// does and how it answers. The tool list is built by the program on every
// request and is not editable, so the model always sees the truth about
// its own abilities.

import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Badge,
  Box,
  Button,
  Group,
  Modal,
  SimpleGrid,
  Stack,
  Text,
  Textarea,
  TextInput,
  Title,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  deletePreset,
  getPresets,
  savePreset,
  setActivePreset,
  type PresetEdit,
  type PresetView,
} from '../api/client'
import { Page, PageHeader } from '../components/layout'
import Loading from '../components/Loading'
import { t } from '../i18n'

// emptyPreset returns a blank custom preset.
function emptyPreset(): PresetEdit {
  return { id: '', name: '', description: '', prompt: '' }
}

// toEdit turns a preset into an editable form.
function toEdit(p: PresetView): PresetEdit {
  return {
    original: p.id,
    id: p.id,
    name: p.name,
    description: p.description,
    prompt: p.prompt,
  }
}

// notify shows a success or failure toast.
function notify(ok: boolean, message: string) {
  notifications.show({ message, color: ok ? 'teal' : 'red', withBorder: true })
}

/** PresetsPage shows the agent working modes. */
export default function PresetsPage() {
  const queryClient = useQueryClient()
  const { data, isLoading, error } = useQuery({ queryKey: ['presets'], queryFn: getPresets })

  const [form, setForm] = useState<PresetEdit | null>(null)
  const [viewing, setViewing] = useState<PresetView | null>(null)

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['presets'] })
  }

  const activate = useMutation({
    mutationFn: (id: string) => setActivePreset(id),
    onSuccess: () => {
      notify(true, t('presets.activated'))
      refresh()
    },
    onError: (err: unknown) => notify(false, (err as Error).message),
  })

  const save = useMutation({
    mutationFn: (edit: PresetEdit) => savePreset(edit),
    onSuccess: () => {
      notify(true, t('presets.saved'))
      setForm(null)
      refresh()
    },
    onError: (err: unknown) => notify(false, (err as Error).message),
  })

  const remove = useMutation({
    mutationFn: (id: string) => deletePreset(id),
    onSuccess: () => {
      notify(true, t('presets.deleted'))
      refresh()
    },
    onError: (err: unknown) => notify(false, (err as Error).message),
  })

  if (isLoading) return <Page><Loading /></Page>
  if (error) return <Page><Loading error={error} /></Page>

  const presets = data?.presets ?? []
  const custom = presets.filter((p) => !p.builtin)
  const builtin = presets.filter((p) => p.builtin)

  return (
    <Page>
      <PageHeader title={t('presets.title')}>
        <Button onClick={() => setForm(emptyPreset())}>{t('presets.create')}</Button>
      </PageHeader>

      <Text size="sm" c="dimmed" mb="md">
        {t('presets.intro')}
      </Text>

      <Stack gap="md">
        <Box>
          <Text size="xs" fw={600} c="dimmed" tt="uppercase" mb="xs">
            {t('presets.builtin')}
          </Text>
          <SimpleGrid cols={{ base: 1, sm: 3 }}>
            {builtin.map((p) => (
              <PresetTile
                key={p.id}
                preset={p}
                onActivate={() => activate.mutate(p.id)}
                onView={() => setViewing(p)}
              />
            ))}
          </SimpleGrid>
        </Box>

        {custom.length > 0 && (
          <Box>
            <Text size="xs" fw={600} c="dimmed" tt="uppercase" mb="xs">
              {t('presets.custom')}
            </Text>
            <SimpleGrid cols={{ base: 1, sm: 3 }}>
              {custom.map((p) => (
                <PresetTile
                  key={p.id}
                  preset={p}
                  onActivate={() => activate.mutate(p.id)}
                  onView={() => setViewing(p)}
                  onEdit={() => setForm(toEdit(p))}
                  onDelete={() => {
                    if (confirm(`${t('presets.confirmDelete')} ${p.name || p.id}?`)) {
                      remove.mutate(p.id)
                    }
                  }}
                />
              ))}
            </SimpleGrid>
          </Box>
        )}
      </Stack>

      {form && (
        <PresetModal
          form={form}
          onChange={setForm}
          onClose={() => setForm(null)}
          onSubmit={() => save.mutate(form)}
          saving={save.isPending}
        />
      )}

      {viewing && (
        <Modal
          opened
          onClose={() => setViewing(null)}
          size="lg"
          title={`${t('presets.promptOf')}: ${viewing.name || viewing.id}`}
        >
          <Stack>
            <Text size="sm" c="dimmed">
              {viewing.builtin ? t('presets.builtinNote') : t('presets.customNote')}
            </Text>
            <pre className="code-block" style={{ maxHeight: '50vh' }}>
              {viewing.prompt}
            </pre>
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setViewing(null)}>
                {t('presets.close')}
              </Button>
            </Group>
          </Stack>
        </Modal>
      )}
    </Page>
  )
}

/** PresetTile shows a mode as a selectable tile. */
function PresetTile({
  preset,
  onActivate,
  onView,
  onEdit,
  onDelete,
}: {
  preset: PresetView
  onActivate: () => void
  onView: () => void
  onEdit?: () => void
  onDelete?: () => void
}) {
  return (
    <div
      className="provider-card"
      style={{
        borderColor: preset.active ? 'var(--accent)' : undefined,
        borderWidth: preset.active ? 2 : 1,
        display: 'flex',
        flexDirection: 'column',
        gap: 10,
      }}
    >
      <Group justify="space-between" align="flex-start" wrap="nowrap">
        <Stack gap={2} style={{ minWidth: 0 }}>
          <Group gap="xs">
            <Title order={5} m={0}>
              {preset.name || preset.id}
            </Title>
            {preset.active && (
              <Badge color="brand" variant="light">
                {t('presets.active')}
              </Badge>
            )}
          </Group>
          <Text size="xs" c="dimmed" className="mono">
            {preset.id}
          </Text>
        </Stack>
      </Group>

      <Text size="sm" style={{ flex: 1 }}>
        {preset.description || '-'}
      </Text>

      <Group gap="xs">
        {preset.active ? (
          <Button variant="light" size="xs" onClick={onView}>
            {t('presets.viewPrompt')}
          </Button>
        ) : (
          <Button variant="light" size="xs" onClick={onActivate}>
            {t('presets.select')}
          </Button>
        )}
        {onEdit && (
          <Button variant="default" size="xs" onClick={onEdit}>
            {t('presets.edit')}
          </Button>
        )}
        {onDelete && (
          <Button variant="light" color="red" size="xs" onClick={onDelete}>
            {t('presets.delete')}
          </Button>
        )}
      </Group>
    </div>
  )
}

/** PresetModal edits a custom preset. */
function PresetModal({
  form,
  onChange,
  onClose,
  onSubmit,
  saving,
}: {
  form: PresetEdit
  onChange: (form: PresetEdit) => void
  onClose: () => void
  onSubmit: () => void
  saving: boolean
}) {
  const patch = (fn: (draft: PresetEdit) => void) => {
    const next = structuredClone(form)
    fn(next)
    onChange(next)
  }

  return (
    <Modal
      opened
      onClose={onClose}
      size="lg"
      title={form.original ? t('presets.editTitle') : t('presets.createTitle')}
    >
      <Stack>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput
            label={t('presets.name')}
            description={t('presets.nameHint')}
            placeholder="Review"
            value={form.name}
            onChange={(e) => patch((d) => { d.name = e.currentTarget.value })}
          />
          <TextInput
            label={t('presets.id')}
            description={t('presets.idHint')}
            placeholder="review"
            value={form.id}
            onChange={(e) => patch((d) => { d.id = e.currentTarget.value })}
            disabled={Boolean(form.original)}
            required
          />
        </SimpleGrid>

        <TextInput
          label={t('presets.description')}
          description={t('presets.descriptionHint')}
          value={form.description}
          onChange={(e) => patch((d) => { d.description = e.currentTarget.value })}
        />

        <Textarea
          label={t('presets.prompt')}
          description={t('presets.promptHint')}
          value={form.prompt}
          onChange={(e) => patch((d) => { d.prompt = e.currentTarget.value })}
          autosize
          minRows={12}
          required
          styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)', fontSize: 12 } }}
        />

        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t('presets.cancel')}
          </Button>
          <Button
            onClick={onSubmit}
            loading={saving}
            disabled={!form.id || form.prompt.trim() === ''}
          >
            {t('presets.save')}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
