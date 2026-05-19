/// <reference types="vitest" />
import {defineConfig} from 'vite'
import {svelte} from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [svelte(), tailwindcss()],
  test: {
    // jsdom gives us a `window` + `localStorage` shim — needed by
    // store.ts, which reads localStorage at module-init time. node
    // environment would throw ReferenceError on import.
    environment: 'jsdom',
    // Tests live next to the source they cover (lib/foo.test.ts).
    include: ['src/**/*.test.ts'],
    // Reset module registry between tests so dynamic re-imports of
    // store.ts can pick up freshly-seeded localStorage. Cheap; only
    // matters for the migration tests.
    isolate: true,
  },
})
