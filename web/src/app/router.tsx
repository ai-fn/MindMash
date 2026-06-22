import { createBrowserRouter, type RouteObject } from 'react-router-dom'

import { LoginScreen } from '../features/auth/LoginScreen'
import { DecksScreen } from '../features/decks/DecksScreen'
import { ProtectedRoute } from './guard'

// Route definitions extracted so tests can build a createMemoryRouter from the same
// route objects rather than duplicating them (see router.test.tsx). No catch-all/404
// route yet — out of scope for this task (see task-3-brief.md).
export const routes: RouteObject[] = [
  {
    path: '/login',
    element: <LoginScreen />,
  },
  {
    path: '/',
    element: <ProtectedRoute />,
    children: [{ index: true, element: <DecksScreen /> }],
  },
]

// Data-router API (react-router-dom v7), used by the real app (providers.tsx).
export const router = createBrowserRouter(routes)
