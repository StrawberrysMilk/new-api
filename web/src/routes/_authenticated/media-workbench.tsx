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
import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'

import { MediaWorkbench } from '@/features/media-workbench'

export const Route = createFileRoute('/_authenticated/media-workbench')({
  validateSearch: z
    .object({
      state: z
        .string()
        .regex(/^[A-Za-z0-9_-]{43}$/)
        .optional(),
      challenge: z
        .string()
        .regex(/^[A-Za-z0-9_-]{43}$/)
        .optional(),
    })
    .refine((value) => Boolean(value.state) === Boolean(value.challenge)),
  component: MediaWorkbenchRoute,
})

function MediaWorkbenchRoute() {
  const search = Route.useSearch()
  return <MediaWorkbench state={search.state} challenge={search.challenge} />
}
