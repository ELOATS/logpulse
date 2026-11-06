import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Route, Routes } from 'react-router'
import './index.css'
import Layout from './components/Layout'
import Dashboard from './pages/Dashboard'
import Logs from './pages/Logs'
import Patterns from './pages/Patterns'
import Anomalies from './pages/Anomalies'
import Ingest from './pages/Ingest'

const queryClient = new QueryClient({
  defaultOptions: { queries: { refetchOnWindowFocus: false, staleTime: 10_000 } },
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Routes>
          <Route element={<Layout />}>
            <Route index element={<Dashboard />} />
            <Route path="logs" element={<Logs />} />
            <Route path="patterns" element={<Patterns />} />
            <Route path="anomalies" element={<Anomalies />} />
            <Route path="ingest" element={<Ingest />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
)
