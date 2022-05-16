import { Flex, Text } from '@siafoundation/design-system'
import {
  useWalletAddresses,
  useWalletBalance,
  useWalletSeedIndex,
  useWalletTransactions,
  useWalletUtxos,
} from '@siafoundation/react-siad'
import { DataPanel } from '../components/DataPanel'
import { useDialog } from '../contexts/dialog'

export function Wallet() {
  const { openDialog } = useDialog()
  const balance = useWalletBalance({
    refreshInterval: 5_000,
  })
  const transactions = useWalletTransactions()
  const utxos = useWalletUtxos()
  const seedIndex = useWalletSeedIndex()
  const addresses = useWalletAddresses()
  // const peers = useSyncerPeers()
  // const connect = useSyncerConnect()
  // const poolTransactions = useTxPoolTransactions({
  //   refreshInterval: 5_000,
  // })
  // const broadcast = useTxPoolBroadcast()
  // const tip = useConsensusTip({
  //   refreshInterval: 1_000,
  // })

  return (
    <Flex gap="3">
      <Flex direction="column" gap="2" css={{ flex: 1, overflow: 'hidden' }}>
        <Text>Advanced</Text>
        <DataPanel title="Wallet balance" data={balance.data} />
        <DataPanel title="Wallet transactions" data={transactions.data} />
        <DataPanel title="Wallet utxos" data={utxos.data} />
        <DataPanel title="Wallet seed index" data={seedIndex.data} />
      </Flex>
      <Flex direction="column" gap="2" css={{ flex: 1, overflow: 'hidden' }}>
        <Text>Advanced</Text>
        <DataPanel
          actions={[
            {
              title: 'Add address',
              onClick: () => openDialog('addAddress'),
            },
          ]}
          title="Wallet addresses"
          data={addresses.data}
        />
      </Flex>
    </Flex>
  )
}
