import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { MediaWorkbench } from '..'

vi.mock('@/lib/api', () => ({ api: { get: vi.fn(), post: vi.fn() } }))

afterEach(cleanup)

function renderWorkbench(state?: string, challenge?: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}><MediaWorkbench state={state} challenge={challenge} /></QueryClientProvider>)
}

describe('Media workbench authorization', () => {
  it('does not authorize when the operator has disabled the integration', async () => {
    vi.mocked(api.get).mockResolvedValue({ data: { success: true, data: { enabled: false, start_url: '' } } })
    renderWorkbench('s'.repeat(43), 'c'.repeat(43))
    expect(await screen.findByText('Media workbench is unavailable. Please contact the administrator.')).toBeTruthy()
    expect(api.post).not.toHaveBeenCalled()
  })

  it('shows an error and does not automatically retry rejected authorization', async () => {
    vi.mocked(api.get).mockResolvedValue({ data: { success: true, data: { enabled: true, start_url: 'https://media.example/api/auth/resnewapi/start' } } })
    vi.mocked(api.post).mockRejectedValue(new Error('Expired session'))
    renderWorkbench('s'.repeat(43), 'c'.repeat(43))
    expect(await screen.findByText('Media workbench is unavailable. Please contact the administrator.')).toBeTruthy()
    await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))
    expect(api.post).toHaveBeenCalledWith('/api/media-workbench/authorize', { state: 's'.repeat(43), challenge: 'c'.repeat(43) })
  })
})
