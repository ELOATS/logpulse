import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    rollupOptions: {
      output: {
        manualChunks: { charts: ['recharts'], vendor: ['react', 'react-dom', 'react-router', '@tanstack/react-query'] },
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': process.env.VITE_API_PROXY ?? 'http://localhost:8080',
    },
  },
})
