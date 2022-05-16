import { useWalletBalance } from '@siafoundation/react-siad'

export function useConnectivity() {
  const w = useWalletBalance()

  const connError = w.error

  console.log(connError)

  // Any error fetching wallet data means daemon is not connected
  const daemon = !connError

  // const wallet = !!w.data?.unlocked
  const wallet = !!w.data

  return {
    all: daemon && wallet,
    connections: daemon,
    daemon,
    wallet,
  }
}

export type Connectivity = ReturnType<typeof useConnectivity>
