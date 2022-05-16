import { Flex } from '@siafoundation/design-system'
import {
  useConsensusTip,
  useSyncerPeers,
  useTxPoolTransactions,
} from '@siafoundation/react-siad'
import { DataPanel } from '../components/DataPanel'

export function Home() {
  // const { openDialog } = useDialog()
  // const balance = useWalletBalance({
  //   refreshInterval: 5_000,
  // })
  // const transactions = useWalletTransactions()
  // const utxos = useWalletUtxos()
  // const seedIndex = useWalletSeedIndex()
  // const addresses = useWalletAddresses()
  const peers = useSyncerPeers()
  // const connect = useSyncerConnect()
  const poolTransactions = useTxPoolTransactions({
    refreshInterval: 5_000,
  })
  // const broadcast = useTxPoolBroadcast()
  const tip = useConsensusTip({
    refreshInterval: 1_000,
  })

  return (
    <Flex gap="3">
      <Flex direction="column" gap="2" css={{ flex: 1, overflow: 'hidden' }}>
        <DataPanel title="Syncer peers" data={peers.data} />
        <DataPanel title="TxPool transactions" data={poolTransactions.data} />
      </Flex>
      <Flex direction="column" gap="2" css={{ flex: 1, overflow: 'hidden' }}>
        <DataPanel title="Consensus tip" data={tip.data} />
      </Flex>
    </Flex>
  )
}
