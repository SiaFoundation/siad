import {
  Dialog,
  DialogContent,
  DialogOverlay,
  DialogPortal,
} from '@siafoundation/design-system'
import { useDialog } from '../../contexts/dialog'
import { AddAddressDialog } from '../AddAddressDialog'
import { PrivacyDialog } from '../PrivacyDialog'

export function Dialogs() {
  const { dialog, closeDialog } = useDialog()

  return (
    <Dialog open={!!dialog} onOpenChange={() => closeDialog()}>
      <DialogPortal>
        <DialogOverlay />
        <DialogContent>
          {dialog === 'privacy' && <PrivacyDialog />}
          {dialog === 'addAddress' && <AddAddressDialog />}
        </DialogContent>
      </DialogPortal>
    </Dialog>
  )
}
