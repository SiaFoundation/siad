import { StrictMode } from 'react'
import * as ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { Layout } from './components/Layout'
import { Providers } from './config/provider'
import { Router } from './config/router'

const root = ReactDOM.createRoot(document.getElementById('root') as HTMLElement)
root.render(
  <StrictMode>
    <BrowserRouter>
      <Providers>
        <Layout>
          <Router />
        </Layout>
      </Providers>
    </BrowserRouter>
  </StrictMode>
)
