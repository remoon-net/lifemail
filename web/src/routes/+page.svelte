<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';

	onMount(() => {
		void goto((resolve as (path: string) => string)('/app') + '/', { replaceState: true });
	});
</script>

<svelte:head>
	<!-- The Go handler redirects first; this is the fallback when the site is
	     published as plain static files behind a reverse proxy. -->
	<meta http-equiv="refresh" content="0; url=/app/" />
	<title>lifemail</title>
</svelte:head>

<div class="flex min-h-dvh items-center justify-center">
	<p class="muted text-sm">正在进入邮箱…</p>
</div>
