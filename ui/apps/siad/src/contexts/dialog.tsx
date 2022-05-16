import React, { createContext, useContext, useCallback, useState } from 'react'

const DialogContext = createContext({} as State)
export const useDialog = () => useContext(DialogContext)

type Props = {
  children: React.ReactNode
}

type Dialog = 'privacy' | 'addAddress'

type State = {
  dialog?: Dialog
  openDialog: (dialog: Dialog) => void
  closeDialog: () => void
}

export function DialogProvider({ children }: Props) {
  const [dialog, setDialog] = useState<Dialog>()

  const openDialog = useCallback(
    (dialog: Dialog) => {
      setDialog(dialog)
    },
    [setDialog]
  )

  const closeDialog = useCallback(() => {
    setDialog(undefined)
  }, [setDialog])

  const value: State = {
    dialog,
    openDialog,
    closeDialog,
  }

  return (
    <DialogContext.Provider value={value}>{children}</DialogContext.Provider>
  )
}
