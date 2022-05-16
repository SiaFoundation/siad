import { Transaction, WalletBalanceResponse } from './types'
import { SWROptions, useGet } from '@siafoundation/react-core'

export function useWalletBalance(options?: SWROptions<WalletBalanceResponse>) {
  return useGet('wallet/balance', options)
}

export function useWalletAddress(options?: SWROptions<string>) {
  return useGet('wallet/address', options)
}

export function useWalletTransactions(options?: SWROptions<Transaction[]>) {
  return useGet('wallet/transactions', options)
}
