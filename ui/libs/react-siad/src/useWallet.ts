import { SWROptions, useGet, usePost } from '@siafoundation/react-core'
import {
  AddressInfo,
  Transaction,
  WalletBalanceResponse,
  WalletUTXOsResponse,
} from './types'

export function useWalletBalance(options?: SWROptions<WalletBalanceResponse>) {
  return useGet('wallet/balance', options)
}

export function useWalletSeedIndex(options?: SWROptions<string>) {
  return useGet('wallet/seedindex', options)
}

export function useWalletAddress(
  address: string,
  options?: SWROptions<string>
) {
  return useGet(`wallet/address/${address}`, options)
}

export function useWalletAddressCreate() {
  return usePost<AddressInfo, { status: number }>('wallet/address', [
    'wallet/addresses',
    'wallet/utxos',
  ])
}

export function useWalletAddresses(options?: SWROptions<string[]>) {
  return useGet('wallet/addresses', options)
}

export function useWalletTransactions(options?: SWROptions<Transaction[]>) {
  return useGet('wallet/transactions', options)
}

export function useWalletUtxos(options?: SWROptions<WalletUTXOsResponse>) {
  return useGet('wallet/utxos', options)
}
