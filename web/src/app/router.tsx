import { createBrowserRouter, type RouteObject } from 'react-router-dom'

import { LoginScreen } from '../features/auth/LoginScreen'
import { RegisterScreen } from '../features/auth/RegisterScreen'
import { BrowseScreen } from '../features/browse/BrowseScreen'
import { DecksScreen } from '../features/decks/DecksScreen'
import { SettingsScreen } from '../features/settings/SettingsScreen'
import { StatsScreen } from '../features/stats/StatsScreen'
import { StudyScreen } from '../features/study/StudyScreen'
import { AppLayout } from './AppLayout'
import { ProtectedRoute } from './guard'
import { NotFoundScreen } from './NotFoundScreen'

// Route definitions extracted so tests can build a createMemoryRouter from the same
// route objects rather than duplicating them (see router.test.tsx).
//
// Shape: ProtectedRoute gates everything below `/` on auth status (see guard.tsx). Inside
// it, AppLayout renders the quiet shell (top bar + nav) and wraps Decks/Browse/Stats/
// Settings via its own <Outlet/> — but NOT study/:deckId, which is a sibling of the
// AppLayout branch so it stays full-bleed/bare (Constraint 1, see task-3-brief.md).
export const routes: RouteObject[] = [
  {
    path: '/login',
    element: <LoginScreen />,
  },
  {
    path: '/register',
    element: <RegisterScreen />,
  },
  {
    path: '/',
    element: <ProtectedRoute />,
    children: [
      {
        element: <AppLayout />,
        children: [
          { index: true, element: <DecksScreen /> },
          { path: 'browse', element: <BrowseScreen /> },
          { path: 'stats', element: <StatsScreen /> },
          { path: 'settings', element: <SettingsScreen /> },
        ],
      },
      { path: 'study/:deckId', element: <StudyScreen /> },
    ],
  },
  {
    path: '*',
    element: <NotFoundScreen />,
  },
]

// Data-router API (react-router-dom v7), used by the real app (providers.tsx).
export const router = createBrowserRouter(routes)
