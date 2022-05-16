import { ThemeProvider } from '@siafoundation/design-system'
import { SettingsProvider } from '@siafoundation/react-core'
import { DialogProvider } from '../contexts/dialog'

type Props = {
  children: React.ReactNode
}

export function Providers({ children }: Props) {
  return (
    <ThemeProvider>
      <DialogProvider>
        <SettingsProvider>{children}</SettingsProvider>
      </DialogProvider>
    </ThemeProvider>
  )
}
