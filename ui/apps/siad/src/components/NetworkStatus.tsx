import {
  Box,
  Flex,
  keyframes,
  Status,
  Tooltip,
} from '@siafoundation/design-system'

const pulse = keyframes({
  '0%': {
    transform: 'scale(1)',
    opacity: 1,
  },
  '30%': {
    transform: 'scale(2)',
    opacity: 0,
  },
  '100%': {
    transform: 'scale(2)',
    opacity: 0,
  },
})

type Props = {
  variant: React.ComponentProps<typeof Status>['variant']
  content?: string
}

export function NetworkStatus({ variant, content }: Props) {
  const el = (
    <Flex
      align="center"
      justify="center"
      css={{ width: '16px', height: '16px' }}
    >
      <Box css={{ position: 'relative' }}>
        <Status variant={variant} />
        <Status
          variant={variant}
          css={{
            animation: `${pulse} 5s infinite`,
            position: 'absolute',
            top: 0,
          }}
        />
      </Box>
    </Flex>
  )

  if (content) {
    return <Tooltip content={content}>{el}</Tooltip>
  }

  return el
}
