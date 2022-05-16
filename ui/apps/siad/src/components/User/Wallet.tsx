import { Flex, Panel, Separator, Text } from '@siafoundation/design-system'
import { useWalletBalance } from '@siafoundation/react-siad'

export function Wallet() {
  const { data: wallet } = useWalletBalance()

  // if (!wallet?.unlocked) {
  //   return null
  // }

  return (
    <Panel
      css={{
        display: 'none',
        '@bp1': {
          display: 'block',
        },
      }}
    >
      <Flex align="center" css={{ height: '$5', padding: '0 $2' }}>
        <Text css={{ fontWeight: '600' }}>
          {(Number(wallet?.siacoins || 0) / Math.pow(10, 24)).toLocaleString()}{' '}
          SC
        </Text>
        <Separator orientation="vertical" pad="1-5" size="1" />
        <Text css={{ fontWeight: '600' }}>
          {Number(wallet?.siafunds || 0).toLocaleString()} SF
        </Text>
      </Flex>
    </Panel>
  )
}
