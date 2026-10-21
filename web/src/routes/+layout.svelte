<script lang="ts">
	import '../app.css';
	import { onMount, type Snippet } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import ToastHost from '#lib/components/ToastHost.svelte';
	import { session } from '#lib/session.svelte';
	import { theme } from '#lib/theme.svelte';

	let { children }: { children: Snippet } = $props();

	theme.init();
	onMount(() => {
		void session.boot();
		const onUnauthorized = () => {
			session.markUnauthenticated();
			void goto((resolve as (path: string) => string)('/login') + '/');
		};
		window.addEventListener('lifemail:unauthorized', onUnauthorized);
		return () => window.removeEventListener('lifemail:unauthorized', onUnauthorized);
	});
</script>

<ToastHost />
{@render children()}
