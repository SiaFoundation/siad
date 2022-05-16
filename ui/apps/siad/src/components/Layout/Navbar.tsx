import {
  AppBar,
  Box,
  Container,
  Flex,
  Heading,
  Logo,
  Panel,
  RRLinkButton,
} from '@siafoundation/design-system'
import { routes } from '../../config/routes'
import { User } from '../User'

export function Navbar() {
  return (
    <AppBar size="2" color="none" sticky>
      <Container size="4">
        <Flex align="center" gap="2" justify="between">
          <Flex
            align="center"
            gap="1-5"
            css={{
              position: 'relative',
              top: '-1px',
            }}
          >
            <Box
              css={{
                transform: 'scale(1.4)',
              }}
            >
              <Logo />
            </Box>
            <Heading
              size="20"
              css={{
                fontWeight: '700',
                display: 'none',
                '@bp1': {
                  display: 'block',
                },
              }}
            >
              siad
            </Heading>
          </Flex>
          <Panel>
            <Flex>
              <RRLinkButton to={routes.wallet}>Wallet</RRLinkButton>
              <RRLinkButton to={routes.home}>Home</RRLinkButton>
              <RRLinkButton to={routes.advanced}>Advanced</RRLinkButton>
            </Flex>
          </Panel>
          <User />
        </Flex>
      </Container>
    </AppBar>
  )
}
