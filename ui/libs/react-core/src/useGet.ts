import useSWR from 'swr'
import { handleResponse } from './handleResponse'
import { SWRError, SWROptions } from './types'
import { useSettings } from './useSettings'
import { getKey } from './utils'

export function useGet<T>(route: string, options?: SWROptions<T>) {
  const { settings } = useSettings()
  return useSWR<T, SWRError>(
    getKey(route, options?.disabled),
    async () => {
      const headers: Record<string, string> = {
        'Content-Type': 'application/json',
      }

      if (settings.password) {
        headers['Authorization'] = 'Basic ' + btoa(`:${settings.password}`)
      }

      const r = await fetch(`/api/${route}`, {
        headers,
      })
      return handleResponse(r)
    },
    options
  )
}
