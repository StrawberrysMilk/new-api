/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
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
import { ERROR_MESSAGES } from '../constants'
import {
  ApiKeyGroupCombobox,
  type ApiKeyGroupOption,
} from './api-key-group-combobox'
import { useApiKeys } from './api-keys-provider'

type ApiKeysBatchGroupDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function ApiKeysBatchGroupDialog(props: ApiKeysBatchGroupDialogProps) {
  const { t } = useTranslation()
  const { triggerRefresh } = useApiKeys()
  const [group, setGroup] = useState('')

  const { data: groupsData, isLoading } = useQuery({
    queryKey: ['user-groups'],
    queryFn: getUserGroups,
    enabled: props.open,
    staleTime: 0,
  })

  const groups = useMemo<ApiKeyGroupOption[]>(
    () =>
      Object.entries(groupsData?.data || {}).map(([key, info]) => ({
        value: key,
        label: key,
        desc: info.desc || key,
        ratio: info.ratio,
      })),
    [groupsData]
  )

  useEffect(() => {
    if (!props.open) {
      setGroup('')
      return
    }
    if (!group && groups.length > 0) {
      setGroup(
        groups.find((option) => option.value === 'default')?.value ||
          groups[0].value
      )
    }
  }, [props.open, group, groups])

  const updateGroupMutation = useMutation({
    mutationFn: batchUpdateApiKeyGroup,
    onSuccess: (result, selectedGroup) => {
      if (!result.success) {
        toast.error(result.message || t(ERROR_MESSAGES.UPDATE_FAILED))
        return
      }

      toast.success(
        t('All API keys switched to group {{group}}', {
          group: selectedGroup,
        })
      )
      props.onOpenChange(false)
      triggerRefresh()
    },
    onError: () => {
      toast.error(t(ERROR_MESSAGES.UNEXPECTED))
    },
  })

  const handleSubmit = () => {
    if (group) updateGroupMutation.mutate(group)
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Switch all groups')}
      description={t('Set the same routing group for all API keys.')}
      contentClassName='sm:max-w-lg'
      footer={
        <>
          <Button
            variant='outline'
            onClick={() => props.onOpenChange(false)}
            disabled={updateGroupMutation.isPending}
          >
            {t('Cancel')}
          </Button>
          <Button
            onClick={handleSubmit}
            disabled={!group || isLoading || updateGroupMutation.isPending}
          >
            {updateGroupMutation.isPending && (
              <Loader2 className='size-4 animate-spin' aria-hidden='true' />
            )}
            {updateGroupMutation.isPending
              ? t('Switching...')
              : t('Confirm switch')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <div className='space-y-2'>
          <div className='text-sm font-medium'>{t('Target group')}</div>
          <ApiKeyGroupCombobox
            options={groups}
            value={group}
            onValueChange={setGroup}
            placeholder={isLoading ? t('Loading...') : t('Select a group')}
            disabled={isLoading || updateGroupMutation.isPending}
          />
        </div>

        <Alert>
          <Layers className='size-4' aria-hidden='true' />
          <AlertTitle>{t('This affects all API keys')}</AlertTitle>
          <AlertDescription>
            {t(
              'Existing API keys will use the selected group immediately. Other settings remain unchanged.'
            )}
          </AlertDescription>
        </Alert>
      </div>
    </Dialog>
  )
}
