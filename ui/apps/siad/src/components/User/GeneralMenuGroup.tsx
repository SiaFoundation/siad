import {
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRightSlot,
  LogoDiscord16,
  Notebook16,
  LicenseGlobal16,
  Information16,
  Link,
} from '@siafoundation/design-system'
import { useDialog } from '../../contexts/dialog'

export function GeneralMenuGroup() {
  const { openDialog } = useDialog()

  return (
    <DropdownMenuGroup>
      <DropdownMenuLabel>General</DropdownMenuLabel>
      <Link href="https://github.com/SiaFoundation/siad" target="_blank">
        <DropdownMenuItem>
          About
          <DropdownMenuRightSlot>
            <Information16 />
          </DropdownMenuRightSlot>
        </DropdownMenuItem>
      </Link>
      <Link href="https://discord.gg/sia" target="_blank">
        <DropdownMenuItem>
          Discord
          <DropdownMenuRightSlot>
            <LogoDiscord16 />
          </DropdownMenuRightSlot>
        </DropdownMenuItem>
      </Link>
      <Link href="https://support.sia.tech" target="_blank">
        <DropdownMenuItem>
          Docs
          <DropdownMenuRightSlot>
            <Notebook16 />
          </DropdownMenuRightSlot>
        </DropdownMenuItem>
      </Link>
      <DropdownMenuItem onSelect={() => openDialog('privacy')}>
        Privacy
        <DropdownMenuRightSlot>
          <LicenseGlobal16 />
        </DropdownMenuRightSlot>
      </DropdownMenuItem>
    </DropdownMenuGroup>
  )
}
