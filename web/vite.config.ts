import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      '/api': 'http://localhost:9147',
    },
  },
  build: {
    outDir: '../internal/server/static',
    emptyOutDir: true,
    rolldownOptions: {
      preserveEntrySignatures: false,
      output: {
        strictExecutionOrder: true,
        codeSplitting: {
          groups: [{
            name: 'monaco',
            test: /[\\/]node_modules[\\/]monaco-editor[\\/]/,
            includeDependenciesRecursively: false,
            entriesAware: true,
            minSize: 30_000,
            maxSize: 450_000,
          }],
        },
      },
    },
  },
})
