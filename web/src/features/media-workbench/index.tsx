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
import { useQuery } from '@tanstack/react-query'
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { api } from '@/lib/api'
import { useMediaWorkbenchConfig } from './use-config'
import { requireServerSuccess } from '@/lib/server-error-message'

export function MediaWorkbench(props: { state?: string; challenge?: string }) {
  const { t } = useTranslation()
  const config = useMediaWorkbenchConfig()
  const hasAuthorization = Boolean(props.state && props.challenge)
  const authorization = useQuery({
    queryKey: ['media-workbench-authorization', props.state, props.challenge],
    queryFn: async (): Promise<string> => {
      const response = await api.post('/api/media-workbench/authorize', {
        state: props.state,
        challenge: props.challenge,
      })
      requireServerSuccess(response.data)
      return response.data.data.redirect_url as string
    },
    enabled: config.data?.enabled === true && hasAuthorization,
    retry: false,
    staleTime: Infinity,
    gcTime: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    meta: { errorToast: false },
  })
  const destination = hasAuthorization
    ? authorization.data
    : config.data?.start_url

  useEffect(() => {
    if (!destination || !config.data?.enabled) return
    // Cross-origin handoff must use a full navigation. Destination is operator-owned.
    const target = new URL(destination)
    const allowed = new URL(config.data.start_url)
    if (target.origin === allowed.origin) window.location.replace(target.href)
  }, [destination, config.data])

  if (config.error || authorization.error || config.data?.enabled === false) {
    return (
      <ErrorState
        description={t(
          'Media workbench is unavailable. Please contact the administrator.'
        )}
      />
    )
  }
  return <LoadingState message={t('Opening media workbench...')} />
}
