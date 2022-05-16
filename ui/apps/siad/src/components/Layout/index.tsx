import {
  Toaster,
  AppBackdrop,
  ScrollArea,
  Container,
} from '@siafoundation/design-system'
import React from 'react'
import { Footer } from './Footer'
import { Navbar } from './Navbar'
import { Dialogs } from './Dialogs'
import { Navigate, useLocation } from 'react-router-dom'
import { routes } from '../../config/routes'
import { useSettings } from '@siafoundation/react-core'

type Props = {
  children: React.ReactNode
}

export function Layout({ children }: Props) {
  const location = useLocation()
  const { settings } = useSettings()

  if (location.pathname !== routes.unlock && !settings.password) {
    return <Navigate to={routes.unlock} replace />
  }

  return (
    <ScrollArea>
      <Dialogs />
      <Toaster />
      <AppBackdrop />
      <Navbar />
      <Container>{children}</Container>
      <Footer />
    </ScrollArea>
  )
}
