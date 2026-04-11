import { defineConfig } from '@farmfe/core'
import postcss from '@farmfe/js-plugin-postcss'

export default defineConfig({
  compilation: {
    input: {
      index: './index.html',
    },
    output: {
      path: './dist',
    },
    define: {
      'import.meta.env.PROD': process.env.NODE_ENV === 'production',
      'import.meta.env.DEV': process.env.NODE_ENV !== 'production',
    },
  },
  server: {
    host: '0.0.0.0',
    port: 3005,
  },
  plugins: ['@farmfe/plugin-react', postcss()],
})
