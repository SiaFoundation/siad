import React, { createContext, useContext } from 'react'
import { useCallback } from 'react'
import useLocalStorageState from 'use-local-storage-state'

type Settings = {
  siaStats: boolean
  password?: string
}

const defaultSettings: Settings = {
  siaStats: true,
  password: undefined,
}

type State = {
  settings: Settings
  setSettings: (settings: Partial<Settings>) => void
}

const SettingsContext = createContext({} as State)
export const useSettings = () => useContext(SettingsContext)

type Props = {
  children: React.ReactNode
  api?: string
}

export function SettingsProvider({ children }: Props) {
  const [settings, _setSettings] = useLocalStorageState('v0/settings', {
    ssr: false,
    defaultValue: defaultSettings,
  })

  const setSettings = useCallback(
    (values: Partial<Settings>) => {
      _setSettings((s) => ({
        ...s,
        ...values,
      }))
    },
    [_setSettings]
  )

  const value = {
    settings,
    setSettings,
  } as State

  return (
    <SettingsContext.Provider value={value}>
      {children}
    </SettingsContext.Provider>
  )
}
