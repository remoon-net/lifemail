<script lang="ts">
	import '../app.css';
	import type { Snippet } from 'svelte';
	import ToastHost from '#lib/components/ToastHost.svelte';
	import { theme } from '#lib/theme.svelte';

	let { children }: { children: Snippet } = $props();

	theme.init();
</script>

<ToastHost />
{@render children()}
