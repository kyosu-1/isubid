import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// ビルド成果物は webapp/public へ出す。参加者はそれをそのまま配信する。
// sourcemap を出さないのは、バンドル上限(300KB)を守るためと、
// ベンチのマニフェスト対象を js/css/html/favicon に絞るため。
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../public',
    emptyOutDir: true,
    sourcemap: false,
  },
  // 開発時は /api をローカルのスタック(nginx :8080)へ流す。
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
})
