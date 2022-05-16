import { toSiacoins } from '@siafoundation/sia-js'
import { useMemo } from 'react'
import BigNumber from 'bignumber.js'
import { useWalletBalance } from '@siafoundation/react-siad'

type Props = {
  isOffer?: boolean
  currency: 'SF' | 'SC'
  value?: BigNumber
}

export function useHasBalance({ currency, isOffer, value }: Props) {
  const { data: wallet } = useWalletBalance()

  return useMemo(() => {
    if (!isOffer || !value) {
      return true
    }
    if (currency === 'SC') {
      // TODO: resolve this issue with generated Currency type
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      return toSiacoins((wallet?.siacoins as any) || 0).gte(value)
    }
    const sfBalance = new BigNumber(wallet?.siafunds || 0)

    return sfBalance.gte(value)
  }, [isOffer, currency, value, wallet])
}
