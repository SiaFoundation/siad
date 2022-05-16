import axios from 'axios'
import { mutate } from 'swr'
import { useSettings } from './useSettings'
import { getKey } from './utils'

type Post<T> = {
  payload: T
  param?: string
}

type PostError = {
  status: number
  error: string
}

export function usePost<P, R>(route: string, deps?: string[]) {
  const { settings } = useSettings()
  return {
    post: async ({ payload, param = '' }: Post<P>) => {
      const headers: Record<string, string> = {
        'Content-Type': 'application/json',
      }
      if (settings.password) {
        headers['Authorization'] = 'Basic ' + btoa(`:${settings.password}`)
      }
      try {
        const response = await axios.post<P, R>(
          `/api/${route}/${param}`,
          payload,
          {
            headers,
          }
        )
        deps?.forEach((dep) => mutate(getKey(dep)))
        return response
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      } catch (e: any) {
        return {
          status: e.response.status,
          error: e.response.data,
        } as PostError
      }
    },
  }
}
