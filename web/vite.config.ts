import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	resolve: {
		alias: {
			'#lib': new URL('./src/lib', import.meta.url).pathname
		}
	},
	plugins: [
		tailwindcss(),
		sveltekit({
			adapter: adapter({
				pages: 'build',
				assets: 'build',
				fallback: undefined,
				precompress: false,
				strict: true
			}),
			paths: {
				// The Go binary serves this build from the site root; the operator
				// console lives under /admin and the account UI under /app.
				base: ''
			},
			prerender: {
				entries: ['*']
			},
			csp: {
				mode: 'hash',
				directives: {
					'default-src': ['self'],
					'script-src': ['self'],
					// Svelte transitions and the announcer use inline style
					// attributes, which hashes cannot cover.
					'style-src': ['self', 'unsafe-inline'],
					'img-src': ['self', 'data:'],
					'connect-src': ['self'],
					'frame-ancestors': ['none']
				}
			}
		})
	]
});
