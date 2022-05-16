import { SWROptions, useGet, usePost } from '@siafoundation/react-core'
import { Transaction, TxpoolBroadcastRequest } from './types'

export function useTxPoolTransactions(options?: SWROptions<Transaction[]>) {
  return useGet('txpool/transactions', options)
}

export function useTxPoolBroadcast() {
  return usePost<TxpoolBroadcastRequest, unknown>('txpool/broadcast', [
    'txpool/transactions',
  ])
}
