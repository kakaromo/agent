import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

// standalone agent (Go) 의 단일 포트 50051 로 REST/SSE/WebSocket 모두 프록시.
export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
	server: {
		proxy: {
			'/api': {
				target: 'http://127.0.0.1:50051',
				changeOrigin: true
			},
			'/ws': {
				target: 'ws://127.0.0.1:50051',
				ws: true,
				changeOrigin: true
			}
		}
	}
});
