import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

/**
 * Reads a dev-only environment variable. The config runs in Node, but the
 * project does not depend on `@types/node`, so `process` is reached through
 * `globalThis`.
 */
function devEnv(name: string): string {
	const env = (globalThis as { process?: { env?: Record<string, string | undefined> } }).process
		?.env;
	return env?.[name] ?? '';
}

// During development the Go server still owns /api; the dev server proxies it so
// the SPA can be edited with HMR against a real backend.
const apiTarget = devEnv('LIFEMAIL_DEV_API') || 'http://127.0.0.1:8080';

// changeOrigin stays off so the forwarded Host matches the browser's Origin,
// which is what the API's same-origin (CSRF) check compares.
const proxy = {
	'/api': { target: apiTarget, changeOrigin: false }
};

export default defineConfig(({ command }) => ({
	resolve: {
		alias: {
			'#lib': new URL('./src/lib', import.meta.url).pathname
		}
	},
	server: { proxy },
	preview: { proxy },
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
			// The policy is baked into the prerendered HTML, so only the build
			// needs it. The dev server injects its own workers and websockets,
			// which a strict policy would block.
			csp:
				command === 'build'
					? {
							mode: 'hash',
							directives: {
								'default-src': ['self'],
								'script-src': ['self'],
								// Svelte transitions and the announcer use inline
								// style attributes, which hashes cannot cover.
								'style-src': ['self', 'unsafe-inline'],
								'img-src': ['self', 'data:'],
								'connect-src': ['self'],
								'frame-ancestors': ['none']
							}
						}
					: undefined
		})
	]
}));
