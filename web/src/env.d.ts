/// <reference types="vite/client" />

import 'vue-router'

declare module 'vue-router' {
  interface RouteMeta {
    /** Page name shown in the 40px header and document title. */
    title: string
    /** 'blank' renders the view outside AppShell (login page). */
    layout?: 'blank'
    /** Routes reachable without a session. */
    public?: boolean
  }
}
