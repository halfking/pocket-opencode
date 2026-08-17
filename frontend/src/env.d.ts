/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_API_BASE?: string
  readonly VITE_APP_ENV?: 'dev' | 'staging' | 'prod'
}
interface ImportMeta {
  readonly env: ImportMetaEnv
}
