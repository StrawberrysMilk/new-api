import { useMutation, useQuery } from '@tanstack/react-query'
import { Layers, Loader2 } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { getUserGroups } from '@/lib/api'

import { batchUpdateApiKeyGroup } from '../api'
import type { ApiKeyFamily } from '../types'
import { ApiKeyGroupCombobox, type ApiKeyGroupOption } from './api-key-group-combobox'
import { useApiKeys } from './api-keys-provider'

type Props = { open: boolean; family: ApiKeyFamily; onOpenChange: (open: boolean) => void }

export function ApiKeysBatchGroupDialog({ open, family, onOpenChange }: Props) {
  const { t } = useTranslation()
  const { triggerRefresh } = useApiKeys()
  const [group, setGroup] = useState('')
  const config = {
    gpt: { label: 'GPT', marker: '-GPT' },
    claude: { label: 'Claude', marker: '-Claude' },
    domestic: { label: '国模', marker: '-国模' },
    gemini: { label: 'Gemini', marker: '-Gemini' },
    grok: { label: 'Grok', marker: '-Grok' },
  }[family]
  const { data, isLoading } = useQuery({ queryKey: ['user-groups'], queryFn: getUserGroups, enabled: open, staleTime: 0 })
  const groups = useMemo<ApiKeyGroupOption[]>(() => Object.entries(data?.data || {}).map(([value, info]) => ({ value, label: value, desc: info.desc || value, ratio: info.ratio })), [data])

  useEffect(() => {
    if (!open) setGroup('')
    else if (!group && groups.length) setGroup(groups.find((item) => item.value === 'default')?.value || groups[0].value)
  }, [open, group, groups])

  const mutation = useMutation({
    mutationFn: batchUpdateApiKeyGroup,
    onSuccess: (result, request) => {
      if (!result.success) return toast.error(result.message || t('Update failed'))
      if (!result.data) return toast.info(t('No API keys with names containing {{marker}} were found.', { marker: config.marker }))
      toast.success(t('Switched {{count}} {{family}} API key(s) to group {{group}}', { count: result.data, family: config.label, group: request.group }))
      onOpenChange(false)
      triggerRefresh()
    },
    onError: () => toast.error(t('Unexpected error')),
  })

  return <Dialog open={open} onOpenChange={onOpenChange} title={t('Switch {{family}} groups', { family: config.label })} description={t('Set the same routing group for API keys whose names contain {{marker}}.', { marker: config.marker })} contentClassName='sm:max-w-lg' footer={<><Button variant='outline' onClick={() => onOpenChange(false)} disabled={mutation.isPending}>{t('Cancel')}</Button><Button onClick={() => group && mutation.mutate({ group, family })} disabled={!group || isLoading || mutation.isPending}>{mutation.isPending && <Loader2 className='size-4 animate-spin' aria-hidden='true' />}{mutation.isPending ? t('Switching...') : t('Confirm switch')}</Button></>}>
    <div className='space-y-4'>
      <div className='space-y-2'><div className='text-sm font-medium'>{t('Target group')}</div><ApiKeyGroupCombobox options={groups} value={group} onValueChange={setGroup} placeholder={isLoading ? t('Loading...') : t('Select a group')} disabled={isLoading || mutation.isPending} /></div>
      <Alert><Layers className='size-4' aria-hidden='true' /><AlertTitle>{t('This affects {{family}} API keys', { family: config.label })}</AlertTitle><AlertDescription>{t('Only API keys whose names contain {{marker}} will be updated. Other API keys and settings remain unchanged.', { marker: config.marker })}</AlertDescription></Alert>
    </div>
  </Dialog>
}
