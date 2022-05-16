import {
  Button,
  DropdownMenuSeparator,
  UserDropdownMenu,
  Warning16,
  Menu16,
} from '@siafoundation/design-system'
import { StatusMenuGroup } from './StatusMenuGroup'
import { GeneralMenuGroup } from './GeneralMenuGroup'
import { useConnectivity } from '../../hooks/useConnectivity'

type Props = React.ComponentProps<typeof Button>

export function UserMenu(props: Props) {
  const { all } = useConnectivity()

  return (
    <UserDropdownMenu
      trigger={
        <Button {...props} css={{ color: all ? '$hiContrast' : '$red10' }}>
          {all ? <Menu16 /> : <Warning16 />}
        </Button>
      }
    >
      <StatusMenuGroup />
      <DropdownMenuSeparator />
      <GeneralMenuGroup />
    </UserDropdownMenu>
  )
}
