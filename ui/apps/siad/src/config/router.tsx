import { Navigate, Route, Routes } from 'react-router-dom'
import { routes } from './routes'
import { Home } from '../pages/Home'
import { Unlock } from '../pages/Unlock'
import { Wallet } from '../pages/Wallet'

export function Router() {
  return (
    <Routes>
      <Route path={routes.home} element={<Home />} />
      <Route path={routes.wallet} element={<Wallet />} />
      <Route path={routes.unlock} element={<Unlock />} />
      <Route path={'/*'} element={<Navigate to={routes.home} />} />
    </Routes>
  )
}
