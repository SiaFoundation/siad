import {
  Code,
  DropdownMenuGroup,
  DropdownMenuLabel,
  Tooltip,
  Locked16,
  Unlocked16,
  Flex,
  Text,
  Paragraph,
} from '@siafoundation/design-system'
import { NetworkStatus } from '../NetworkStatus'
import { useConnectivity } from '../../hooks/useConnectivity'

export function StatusMenuGroup() {
  const { daemon, wallet } = useConnectivity()

  return (
    <DropdownMenuGroup>
      <DropdownMenuLabel>Status</DropdownMenuLabel>
      <Flex justify="between" gap="3" css={{ padding: '$1 $2' }}>
        <Tooltip
          content={
            wallet ? (
              <Flex css={{ padding: '$1' }}>
                <Text>Unlocked</Text>
              </Flex>
            ) : (
              <Flex css={{ padding: '$1', maxWidth: '400px' }}>
                <Paragraph size="14">
                  Locked - unlock daemon wallet to use siad.
                </Paragraph>
              </Flex>
            )
          }
        >
          <Flex direction="column" gap="1" align="center">
            <Flex
              css={{
                color: wallet ? '$green10' : '$red10',
              }}
            >
              {wallet ? <Unlocked16 /> : <Locked16 />}
            </Flex>
            <Code variant="gray">wallet</Code>
          </Flex>
        </Tooltip>
        <Tooltip
          content={
            daemon ? (
              <Flex css={{ padding: '$1' }}>
                <Text>Connected</Text>
              </Flex>
            ) : (
              <Flex css={{ padding: '$1', maxWidth: '400px' }}>
                <Text>Disconnected</Text>
              </Flex>
            )
          }
        >
          <Flex direction="column" gap="1" align="center">
            <NetworkStatus variant={daemon ? 'green' : 'red'} />
            <Code variant="gray">daemon</Code>
          </Flex>
        </Tooltip>
      </Flex>
    </DropdownMenuGroup>
  )
}
