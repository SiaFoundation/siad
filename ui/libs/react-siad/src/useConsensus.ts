import { SWROptions, useGet } from '@siafoundation/react-core'
import { WalletBalanceResponse } from './types'

export function useConsensusTip(options?: SWROptions<WalletBalanceResponse>) {
  return useGet('consensus/tip', options)
}
