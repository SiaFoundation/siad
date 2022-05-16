// import {
//   Flex,
//   Panel,
//   Separator,
//   Text,
//   Tooltip,
// } from '@siafoundation/design-system'
// import { Fragment } from 'react'
// import { useConnectivity } from '../../hooks/useConnectivity'
// import {
//   useSiaStatsNetworkStatus,
//   useSettings,
// } from '@siafoundation/react-core'
// import { NetworkStatus } from '../NetworkStatus'

export function Footer() {
  // const { daemon } = useConnectivity()
  // const { data: siaStats } = useSiaStatsNetworkStatus()
  // const { data: consensus, error: errorC } = useConsensus()
  // const { data: wallet } = useWallet()

  // const isSynced = consensus?.synced

  // const color = errorC ? 'red' : isSynced ? 'green' : 'yellow'

  return null
  // return (
  //   <Panel
  //     css={{
  //       position: 'fixed',
  //       bottom: '$2',
  //       right: '$3',
  //     }}
  //   >
  //     <Flex align="center" css={{ padding: '$1-5' }}>
  //       {daemon && (
  //         <Fragment>
  //           <Tooltip content="Current transaction fee">
  //             <Text size="12" css={{ fontFamily: '$mono', lineHeight: '1' }}>
  //               {((Number(wallet?.dustthreshold) / Math.pow(10, 24)) * 1024) /
  //                 0.001}{' '}
  //               mS / KB
  //             </Text>
  //           </Tooltip>
  //           <Separator pad="1-5" size="1" orientation="vertical" />
  //           <Tooltip
  //             content={
  //               settings.siaStats
  //                 ? `Block height: ${consensus?.height} / ${siaStats?.block_height}`
  //                 : 'Block height'
  //             }
  //           >
  //             <Text size="12" css={{ fontFamily: '$mono' }}>
  //               {consensus?.height}
  //             </Text>
  //           </Tooltip>
  //           <Separator pad="1-5" size="1" orientation="vertical" />
  //         </Fragment>
  //       )}
  //       <NetworkStatus
  //         variant={color}
  //         content={!daemon ? 'Disconnected' : isSynced ? 'Synced' : 'Syncing'}
  //       />
  //     </Flex>
  //   </Panel>
  // )
}
